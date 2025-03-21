# DCR Configuration

This repository contains Pulumi configuration for various dictyBase services.

## Kubernetes Secret Variables

Below is a list of variables that these projects retrieve from Kubernetes Secret objects:

### Event Messenger [Secrets are created in the event-messenger pulumi project]
- `EMAIL_DOMAIN` - from secret key `eventMessenger.email.domain`
- `EMAIL_SENDER_NAME` - from secret key `eventMessenger.email.senderName`
- `EMAIL_SENDER` - from secret key `eventMessenger.email.sender`
- `EMAIL_CC` - from secret key `eventMessenger.email.cc`
- `PUBLICATION_API_ENDPOINT` - from secret key `endpoints.publication`
- `MAILGUN_API_KEY` - from secret key `eventMessenger.email.apiKey`
- `GITHUB_OWNER` - from secret key `eventMessenger.github.owner`
- `GITHUB_REPOSITORY` - from secret key `eventMessenger.github.repository`
- `GITHUB_TOKEN` - from secret key `eventMessenger.github.token`

### Logto [Uses existing secrets] -> Postgres Secret
- `PGPASSWORD` - from secret key `password`
- `DBUSER` - from secret key `username`

### GraphQL Server [Uses existing secrets] -> Minio Secret
- Minio credentials:
  - `SECRET_KEY` - from secret key specified in `secrets.minio.passKey`
  - `ACCESS_KEY` - from secret key specified in `secrets.minio.userKey`

### Redis Backup [Uses existing secrets] -> Backup Secret
- `RESTIC_PASSWORD` - from secret key `resticPass`
- `GOOGLE_PROJECT_ID` - from secret key `gcsProject`
- GCS credentials from a mounted secret volume

### ArangoDB Backup [Uses existing secrets] -> Backup Secret
- `PASSWORD` - from secret key `password` (ArangoDB password)
- `RESTIC_PASSWORD` - from secret key `resticPass`
- `GOOGLE_PROJECT_ID` - from secret key `gcsProject`
- GCS credentials from a mounted secret volume - from secret 

### Load Content from S3 [Uses existing secrets] -> Minio Secret
- `ACCESS_KEY` - from secret key specified in `minioSecret.userKey`
- `SECRET_KEY` - from secret key specified in `minioSecret.passKey`

### ArangoDB Dataloader [Uses existing secrets] -> ArangoSecret, Minio Secret
- `ARANGODB_USER` - from secret key specified in `arangodbSecret.userKey`
- `ARANGODB_PASS` - from secret key specified in `arangodbSecret.passKey`

- `ACCESS_KEY` - from secret key specified in `minioSecret.userKey`
- `SECRET_KEY` - from secret key specified in `minioSecret.passKey`

### Create ArangoDB Databases [Secrets are created in the create-arangodb-databases pulumi project]
- `ARANGODB_PASSWORD` - from secret key specified in `arangodbCredentials.passKey`
