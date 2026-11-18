# Filestash Links and SMTP

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [Admin Console](#admin-console)
- [SMTP](#smtp)
- [Upload Link for a Collaborator](#upload-link-for-a-collaborator)
- [Public Link](#public-link)
- [Revocation](#revocation)

## Admin Console

`just filestash admin` opens a port-forward to Service `filestash` and prints `http://localhost:8334/admin`. Log in with the admin password from `create-secret`. The internet cannot reach `/admin`. Refer to [Admin access and route](admin-access.md).

## SMTP

An upload link with an email list sends a six-character code by email. Without SMTP, the link fails with `Couldn't send email`. Set the `email` fields (server, port, username, password, from) in the admin console settings. Filestash encrypts the password in `config.json` with `CONFIG_SECRET`. Do not put SMTP values in Git.

## Upload Link for a Collaborator

1. Log in to the file manager as `filestash-intake`.
2. Open `/uploads/<collaborator>/`. Make the folder if it does not exist.
3. Make a share with **upload** only, the collaborator email in the user list, and an expiry date.
4. Send the link. The collaborator enters the email and the code, then uploads. The collaborator cannot list or read files.

`just filestash audit-shares` fails on an upload link with no expiry.

## Public Link

1. Log in as `filestash-public`.
2. Make one share on `/downloads/` with **read** only and no expiry.
3. Publish the link (`https://<filestash-host>/s/<id>`). Any person can browse and download.

## Revocation

- Delete a link in the share list. The next request on the link fails.
- A new secret key for a MinIO user stops every link made with the old key. Refer to [Key Rotation](users-and-policies.md#key-rotation).
- Revocation cannot recall a file that a person downloaded before.
