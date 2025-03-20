# DCR Configuration

This repository contains Pulumi configuration for various dictyBase services.

## Kubernetes Secret Variables

Below is a list of variables that these projects retrieve from Kubernetes Secret objects:

### Event Messenger [Secrets are created in the pulumi project]
- `EMAIL_DOMAIN` - from secret key `eventMessenger.email.domain`
- `EMAIL_SENDER_NAME` - from secret key `eventMessenger.email.senderName`
- `EMAIL_SENDER` - from secret key `eventMessenger.email.sender`
- `EMAIL_CC` - from secret key `eventMessenger.email.cc`
- `PUBLICATION_API_ENDPOINT` - from secret key `endpoints.publication`
- `MAILGUN_API_KEY` - from secret key `eventMessenger.email.apiKey`
- `GITHUB_OWNER` - from secret key `eventMessenger.github.owner`
- `GITHUB_REPOSITORY` - from secret key `eventMessenger.github.repository`
- `GITHUB_TOKEN` - from secret key `eventMessenger.github.token`

### Logto
- `PGPASSWORD` - from secret key `password`
- `DBUSER` - from secret key `username`

### GraphQL Server
- Variables related to authentication:
  - Auth app ID from secret key `graphql.auth.appId`
  - Auth app secret from secret key `graphql.auth.appSecret`
  - JWKS URI from secret key `graphql.auth.jwksURI`
  - JWT audience from secret key `graphql.auth.jwtAudience`
  - JWT issuer from secret key `graphql.auth.jwtIssuer`
- Minio credentials:
  - `SECRET_KEY` - from secret key specified in `secrets.minio.passKey`
  - `ACCESS_KEY` - from secret key specified in `secrets.minio.userKey`

### Redis Backup
- `RESTIC_PASSWORD` - from secret key `resticPass`
- `GOOGLE_PROJECT_ID` - from secret key `gcsProject`
- GCS credentials from a mounted secret volume

### ArangoDB Backup
- `PASSWORD` - from secret key `password` (ArangoDB password)
- `RESTIC_PASSWORD` - from secret key `resticPass`
- `GOOGLE_PROJECT_ID` - from secret key `gcsProject`
- GCS credentials from a mounted secret volume

### Load Content from S3
- `ACCESS_KEY` - from secret key specified in `minioSecret.userKey`
- `SECRET_KEY` - from secret key specified in `minioSecret.passKey`

### ArangoDB Dataloader
- `ARANGODB_USER` - from secret key specified in `arangodbSecret.userKey`
- `ARANGODB_PASS` - from secret key specified in `arangodbSecret.passKey`
- `ACCESS_KEY` - from secret key specified in `minioSecret.userKey`
- `SECRET_KEY` - from secret key specified in `minioSecret.passKey`

### Create ArangoDB Databases
- `ARANGODB_PASSWORD` - from secret key specified in `arangodbCredentials.passKey`
