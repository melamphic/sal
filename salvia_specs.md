Salvia

Modules
Form engine
Policy engine
Audio to forms
User management ( staff, subject ) 
Marketplace
Compliance engine ( export, trails, reports )

Form engine

This is a custom form builder inside salvia, where we expect the clinics to create all the forms they use on a daily basis in their clinics. The form builder is really easy to use inspired by google forms, the forms vary with its rich field types, the field types includes all the basic form fields we can imagine, text, long text, numbers, decimals, sliders, button groups, percentage, images and much more. The flutter web/mobile app will handle the UI on displaying these fields as a form, since we use our own custom off_ide package with gives us the VScode like UI we can play with the tabs, being split 

Each form has a main prompt this is used so that the ai gets more context on what the form actually is and this is user given additional to our system prompts. Next each filed needs a field name, then the type and an advanced mode turned on by a simple toggle the user can add a per field prompt explaining what they plan to see in this field when the form is generate. Each field can be marked mandatory or not, and even skipped. 

Linking policies to forms, the user can link policies into forms. Since we have a policy engine the user can link multiple policies to a single form. 

Each form has a versioning system since evidence is something we take very seriously, now these versions are same as an app-release min major and patch version , each change is shown as a small summary if the user wants to get into the history of the form, who created it who made the edit, time, date, user name, etc with each status.

Now the policies linking since we aim for compliace, each form before being published to the clinic for operations the user has an optional button to check is this form compliant with the policy, or does this form they created satisfy the policy in place, this wil be just a text output with points the user may choose to follow this is also saveed as a patch version with a policy tag or (policy) like that with what the ai retuened. And then they can again edit etc or leave it. Then they can draft it or then post it ie submit/publish to the clinic.

For organizing the forms better simple folders can be created just to group them only one lvl of folders are allowed basically a folder could be surgval and it could include or user can add that to the folder. 

The forms can be rolled back since we have versions we need to allow rollbacks, the user can rollback any version, this will also be a major change a (roll back) can be seen and we ask a optional reason for rollback which will also be shown and then on submit the rolled back is live.

Deleting forms, we dont allow deletes. Instead they have a an option to retire the form or unpublish ie taking the form out of operation this same as rollback a major version which is noted and we also ask a reason for unpublishing. 

If ai was processing forms, or there we forms under process and a rollback or a decommission occurs we don't pull them back the process is completed but we show an acknowledgement like “after rollback” or “before rollback” or “before decommission” or before “version change” 

We also allow the users to see ie preview a document made from the form they make, since these forms will need export ot even pritedd. We have an option to view the form in a PDF version of the form, this is made from the form we made and using a library in flutter itsef. 

Now this can be printed with the empty values and sliders and other UI like fields go to their equivalent logical normal paper form version. The header and footer can be customized with the color theme and the font, the header we would love to see the clinic name logo contact etc form name etc, the footer any text option then the version of the form and who approved the version right.

This can be edited in a different tab where the font style ie typorgrapy and user uploads the clinicn logo colrs etc.  this is a global setting where all forms going forward will reflect this style change, this also need to be save like this is a patch style and saves with the version ie a version bump.


Policy Engine

The policy engine is another important module inside salvia, each clinic has thier own internal policies these are usually made to be compliant with the rule/framework they follow in its its NABH and BESTPRACTICE in NZ and VMR etc and the list goes on different domains.

My vision was it being like notion where each are blocs and we dont know how each org creates their polices right soo since its flutter APPflowy’s OSS package can be used right Z

The plan is since these orgs will already have policies in place we want to get them into salvia’s policy engine like the option is a add on available at the middle tier or a purchasable add on at the beginner tier where they can upload all the docs or pdf’s and we will turn them or import them to salvia 

These too have the version system same as te forms, and also a simple folder like organization as the forms have, now there is additional feature where a block or content in the policy engine can be makrked as a clause like enforceable with medium, low and high parity this is a mental model like, must follow, maybe follow, and try to follow like that ok.

Now these policies xan also be exported same as forms into document same header foter ting i told right, now the versions system also applies for policies and if a policy was pulled off of the system or and if a form had that policy linked with it the policy will be removed ie the link broken and a version major version update in the form like this policy was unlinked right sue to this 

Soo thats the policy engine

We have a later planned featue where a RAG chat for the staff to query on the policies of the org so that they have 0 doubts. Etc 
AUDIO TO FORMS

This is the core of the platform users will record audio user being a staff, this could be a doctor, a vet, a nurse, an agedcare staff, now this is with context to a subject, subject is a patient in a clinical setting an animal in a vet setting and and elderly person in an aged care.

This audio is then hooked to a form by the staff, the staff select the subject record the audio, select a form then the processing starts, they can select one to many forms we restrict to 3 forms, now 

This sent to our server where deepgram nova medical transcribes the audio to text and then this text is used by open ai model to then give us structured output ie the forms the ai can leave a field empty too we are fine with that and confidence sore is also amde using a determisnitc approach.

1. Evidence exists
evidence ∈ transcript
2. Transformation allowed
transformation ∈ allowed_transformations
3. Inference control
if allow_inference == false && transformation == inference → reject
4. Confidence
confidence = avg(word_confidence(evidence words))
5. Threshold
if confidence < min_confidence → reject


An example would be this

{ "drug_name": { "value": "amoxicillin", "evidence": "amoxicillin", "transformation": "direct" } }

Now the form can be viewed by the user, there are 2 options either the user can check the form for mistakes right now or leave it and come back later its fine we have a separate review section right. 

The user sees the filled form with hints next to it like the score then some fields the ai can leave empty saying no context which is also fine. Now the form below also shows the original transcript and the audio too, the user can edit the fields ie the answers which is fine, but we need the history of the edit what changed and we need all the changes etc ok just like versioning

The form also shows how much policy percentage was acheived like policy followed or satisfied right with the answers etc and the transcript, the user needs to explicitly check “ reviewed by “ ie and with their name they can post this note ok thats it 
Now this note comes ups in the timeline, we trigger or add the document degeneration to our job quee either we do it in batches etc 

This shows up int the subjects profile with all the edits what changed history etc and other staff with access can also see this with the og audio, transcript ai flagged stuff policy etc.

The second method the staff can folow is in the app or from the dash web the review all section will contain all the forms for that say and they can go one by onne and verify each and publish it now the timeline primary time is the time it was recorded and to see all the micro changes of that audio maybe value edited policy this checked and this chgae dhtat chaged etc, to see that the person has to go into the details of this single timeline entry and see the whole metamorphosis of this audio into the form, the time recorded changes etc when it was reviewed when it was published etc.

This is where users with persian can again edit the form values and add reasons and also delete we dont do delete instead we do an archive or something and a reason is must with which user did it right. 

The timeline for theat patient is made the org admin can see the clciini ctimeine too 

Everything realtime too. 

There is an option where they can fill the form manually without using ai completely ie no audio ai which is also supported by us.

Deleted or archived notes are not showed on timeline and the general context fo the patient but can be viwerd if that filter is check ed ie show archived or retracted right.


Before published the form obviously its rules will be checked like is al fields ented and mandatory filled etc, and a check policy alighnet button is present before the i acknowledge step and i reviewed this step which is out human in the loop and then just do it in their name. And publish.
