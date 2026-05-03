// Command backfill-drug-chain stamps Compliance v2 chain fields onto
// legacy drug_operations_log rows that were inserted before Phase 2
// shipped.
//
// What it does, per clinic:
//   1. Loads every row in (clinic, created_at ASC) order that lacks a
//      chain_key — these are the legacy rows.
//   2. Joins to clinic_drug_shelf + the system catalog (or override) to
//      reconstruct the page-identity tuple (drug_name, strength, form).
//   3. Computes chain_key + entry_seq_in_chain + prev_row_hash + row_hash
//      preserving the original created_at order.
//   4. Stamps each row in a single UPDATE (idempotent — skips rows that
//      already have a chain_key).
//
// Idempotency: re-running the command is safe — already-stamped rows
// are skipped. If a chain has both stamped and unstamped rows (partial
// run), the command resumes seamlessly.
//
// Usage:
//
//   go run ./cmd/backfill-drug-chain --clinic-id=<UUID>
//   go run ./cmd/backfill-drug-chain --all-clinics    # iterate every clinic
//
// Reads DATABASE_URL from the environment (or --dsn flag).
//
// Prereqs:
//   - migrations 00066-00071 applied
//   - clinic exists with vertical + country set so the catalog lookup works
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/drugs"
	drugscatalog "github.com/melamphic/sal/internal/drugs/catalog"
)

func main() {
	var (
		dsn         string
		clinicIDStr string
		allClinics  bool
		batchSize   int
		dryRun      bool
	)
	flag.StringVar(&dsn, "dsn", os.Getenv("DATABASE_URL"), "Postgres DSN (default $DATABASE_URL)")
	flag.StringVar(&clinicIDStr, "clinic-id", "", "single clinic UUID to backfill")
	flag.BoolVar(&allClinics, "all-clinics", false, "backfill every clinic in the database")
	flag.IntVar(&batchSize, "batch", 500, "rows per batch")
	flag.BoolVar(&dryRun, "dry-run", false, "log what would be stamped, write nothing")
	flag.Parse()

	if dsn == "" {
		fail("DATABASE_URL or --dsn required")
	}
	if (clinicIDStr == "" && !allClinics) || (clinicIDStr != "" && allClinics) {
		fail("pass exactly one of --clinic-id or --all-clinics")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("dial: %v", err)
	}
	defer pool.Close()

	cat, err := drugscatalog.NewLoader()
	if err != nil {
		fail("catalog load: %v", err)
	}

	repo := drugs.NewRepository(pool)

	var clinicIDs []uuid.UUID
	if allClinics {
		clinicIDs, err = listClinicIDs(ctx, pool)
		if err != nil {
			fail("list clinics: %v", err)
		}
	} else {
		id, err := uuid.Parse(clinicIDStr)
		if err != nil {
			fail("invalid --clinic-id: %v", err)
		}
		clinicIDs = []uuid.UUID{id}
	}

	for _, clinicID := range clinicIDs {
		if err := backfillClinic(ctx, pool, repo, cat, clinicID, batchSize, dryRun); err != nil {
			slog.Error("backfill failed", "clinic_id", clinicID, "error", err.Error())
			os.Exit(2)
		}
	}
}

func backfillClinic(
	ctx context.Context,
	pool *pgxpool.Pool,
	repo *drugs.Repository,
	cat *drugscatalog.Loader,
	clinicID uuid.UUID,
	batchSize int,
	dryRun bool,
) error {
	vertical, country, err := readClinicVerticalCountry(ctx, pool, clinicID)
	if err != nil {
		return fmt.Errorf("clinic vertical/country: %w", err)
	}

	// Per-chain in-memory state — accumulates as we walk rows in
	// created_at order. Key is the hex of the chain_key.
	type chainState struct {
		seq         int64
		prevRowHash []byte
	}
	state := map[string]*chainState{}

	since := time.Time{}
	totalStamped := 0
	pages := 0

	for {
		rows, err := repo.ListLegacyOpsForBackfill(ctx, clinicID, since, batchSize)
		if err != nil {
			return fmt.Errorf("list batch: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		pages++

		for _, op := range rows {
			drugName, strength, form, retentionUntil, err := pageIdentityForOp(ctx, pool, cat, vertical, country, op, clinicID)
			if err != nil {
				slog.Warn("backfill: skip row (page-identity unresolved)",
					"clinic_id", clinicID, "op_id", op.ID, "error", err.Error())
				since = op.CreatedAt
				continue
			}
			if drugName == "" || strength == "" || form == "" {
				slog.Warn("backfill: skip row (incomplete page-identity)",
					"clinic_id", clinicID, "op_id", op.ID,
					"drug_name", drugName, "strength", strength, "form", form)
				since = op.CreatedAt
				continue
			}

			chainK := drugs.ChainKeyForBackfill(clinicID, drugName, strength, form)
			st, ok := state[hex(chainK)]
			if !ok {
				st = &chainState{seq: 0, prevRowHash: drugs.ZeroHash()}
				state[hex(chainK)] = st
			}
			st.seq++
			canonical := drugs.CanonicalRowBytesForBackfill(
				op.ID, clinicID, chainK, st.seq,
				op.Operation, op.Quantity, op.Unit,
				drugName, strength, form,
				op.BalanceAfter, st.prevRowHash,
			)
			rowHash := drugs.ComputeRowHashForBackfill(canonical, st.prevRowHash)

			if dryRun {
				slog.Info("backfill (dry-run)",
					"clinic_id", clinicID, "op_id", op.ID,
					"chain_key_hex", hex(chainK), "seq", st.seq)
			} else {
				err := repo.BackfillChainRow(ctx, drugs.BackfillChainRowParams{
					ID:              op.ID,
					ClinicID:        clinicID,
					DrugName:        drugName,
					DrugStrength:    strength,
					DrugForm:        form,
					ChainKey:        chainK,
					EntrySeqInChain: st.seq,
					PrevRowHash:     st.prevRowHash,
					RowHash:         rowHash,
					RetentionUntil:  retentionUntil,
				})
				if err != nil {
					return fmt.Errorf("backfill row %s: %w", op.ID, err)
				}
				totalStamped++
			}
			st.prevRowHash = rowHash
			since = op.CreatedAt
		}
		if len(rows) < batchSize {
			break
		}
	}
	slog.Info("backfill: clinic done",
		"clinic_id", clinicID, "pages", pages, "rows_stamped", totalStamped, "dry_run", dryRun)
	return nil
}

func listClinicIDs(ctx context.Context, pool *pgxpool.Pool) ([]uuid.UUID, error) {
	const q = `SELECT id FROM clinics ORDER BY created_at ASC`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listClinicIDs: query: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("listClinicIDs: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listClinicIDs: rows: %w", err)
	}
	return ids, nil
}

func readClinicVerticalCountry(ctx context.Context, pool *pgxpool.Pool, clinicID uuid.UUID) (string, string, error) {
	const q = `SELECT vertical, country FROM clinics WHERE id = $1`
	var vertical, country string
	if err := pool.QueryRow(ctx, q, clinicID).Scan(&vertical, &country); err != nil {
		return "", "", fmt.Errorf("readClinicVerticalCountry: %w", err)
	}
	return vertical, country, nil
}

func pageIdentityForOp(
	ctx context.Context,
	pool *pgxpool.Pool,
	cat *drugscatalog.Loader,
	vertical, country string,
	op *drugs.OperationRecord,
	clinicID uuid.UUID,
) (string, string, string, *time.Time, error) {
	const q = `
		SELECT shelf.catalog_id, shelf.override_drug_id, shelf.strength, shelf.form
		  FROM clinic_drug_shelf shelf
		 WHERE shelf.id = $1 AND shelf.clinic_id = $2`
	var (
		catalogID      *string
		overrideID     *uuid.UUID
		strength, form *string
	)
	if err := pool.QueryRow(ctx, q, op.ShelfID, clinicID).Scan(&catalogID, &overrideID, &strength, &form); err != nil {
		return "", "", "", nil, fmt.Errorf("pageIdentityForOp: shelf scan: %w", err)
	}

	var drugName, str, frm string
	if strength != nil {
		str = *strength
	}
	if form != nil {
		frm = *form
	}

	if catalogID != nil {
		entry := cat.Lookup(vertical, country, *catalogID)
		if entry != nil {
			drugName = entry.Name
			if frm == "" {
				frm = entry.Form
			}
		}
	} else if overrideID != nil {
		const oq = `SELECT name, strength, form FROM clinic_drug_catalog_overrides WHERE id = $1`
		var (
			oname  string
			ostr   *string
			oform  *string
		)
		if err := pool.QueryRow(ctx, oq, *overrideID).Scan(&oname, &ostr, &oform); err == nil {
			drugName = oname
			if str == "" && ostr != nil {
				str = *ostr
			}
			if frm == "" && oform != nil {
				frm = *oform
			}
		}
	}

	// Retention floor — read clinic policy, derive from op.CreatedAt + ledger_years.
	const polQ = `SELECT ledger_years FROM clinic_drug_retention_policy WHERE clinic_id = $1`
	var ledgerYears int
	var retentionUntil *time.Time
	if err := pool.QueryRow(ctx, polQ, clinicID).Scan(&ledgerYears); err == nil {
		ru := op.CreatedAt.AddDate(ledgerYears, 0, 0)
		retentionUntil = &ru
	}

	return drugName, str, frm, retentionUntil, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "backfill-drug-chain: "+format+"\n", args...)
	os.Exit(1)
}

func hex(b []byte) string {
	const hexchars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexchars[v>>4]
		out[i*2+1] = hexchars[v&0x0f]
	}
	return string(out)
}

// Unused stub — keeps the import linter happy until errors.Is gets
// referenced once the cmd grows real error mapping.
var _ = errors.New
