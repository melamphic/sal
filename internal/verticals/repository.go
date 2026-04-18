package verticals

// The verticals module has no SQL: schemas are compiled into the binary
// via domain.VerticalSchemas. This file exists so the package conforms
// to the 4-file module layout (handler/service/repository/routes) and
// makes explicit that adding a schema is a code change, not a DB migration.
