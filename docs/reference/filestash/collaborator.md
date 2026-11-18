# What a Collaborator Sees

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

A collaborator needs no account. The collaborator gets one upload link and
uses one email address. Send the collaborator the text in
[Invitation Text](#invitation-text).

## Table of Contents

- [Steps for the Collaborator](#steps-for-the-collaborator)
- [What the Collaborator Cannot Do](#what-the-collaborator-cannot-do)
- [Staff Check of the Upload](#staff-check-of-the-upload)
- [Invitation Text](#invitation-text)
- [Problems the Collaborator Can Report](#problems-the-collaborator-can-report)

## Steps for the Collaborator

1. The collaborator opens the upload link (`https://<filestash-host>/s/<id>`).
2. The page asks for an email address. The collaborator types the address that staff put on the link.
3. Filestash sends an email with the subject `Your verification code`. The email holds a six-character code. The code expires after 10 minutes.
4. The page shows `We've sent you a message with a verification code` until the collaborator types the code.
5. The page shows an upload button and no file list. The collaborator selects the files or drops them on the page.
6. Each file goes to the collaborator's folder in `uploads`, for example `uploads/alice/`.

## What the Collaborator Cannot Do

- See, open, or download any file. This rule also applies to the files that the collaborator uploaded.
- Use the link with a different email address.
- Use the link after its expiry date, or after staff delete the link.
- Reach the folder of another collaborator.

If the collaborator uploads a file with the same name twice, the second file
can replace the first. Ask collaborators to use unique file names.

## Staff Check of the Upload

The collaborator cannot see the files after the upload. Staff confirm the
upload for the collaborator.

1. Get the operator keys with `just filestash login-keys --role operator`.
2. Run `just filestash admin`. Open `http://localhost:8334/` and log in as `filestash-operator`.
3. Open `/uploads/<collaborator>/` and check the files.
4. Reply to the collaborator with the list of files that arrived.

**Never make a link in a `filestash-operator` session.** The link gets the
read and delete rights of that key. To publish reviewed files, use
[`just filestash publish`](publish.md#just-filestash-publish).

## Invitation Text

> You can send your files to the dictyBase project with this link:
> `<upload link>`
>
> 1. Open the link and enter the email address `<email>`.
> 2. Enter the six-character code from the email "Your verification code". The code expires after 10 minutes.
> 3. Select your files, or drop them on the page.
>
> The link expires on `<date>`. You cannot see files after you upload them.
> Use a unique name for each file. If you have a problem, reply to this email.

## Problems the Collaborator Can Report

| Report | Cause | Staff action |
|---|---|---|
| No email arrives | Staff did not set SMTP, or the email is in a spam folder | Check SMTP in the admin console. Refer to [Links and SMTP](shares.md#smtp). |
| `Couldn't send email` | SMTP failure | Same as above. |
| The code does not work | The code expired after 10 minutes | Ask the collaborator to request a new code. |
| Filestash refuses the email address | The address is not on the link | Add the address to the link, or make a new link. |
| The link does not open | The link expired or staff deleted it | Make a new link. |
