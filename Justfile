mod gcp-analysis 'just_modules/analysis.justfile'
mod gcp-sa 'just_modules/sa.justfile'
mod gcp-role 'just_modules/role.justfile'
mod gcp-api 'just_modules/api.justfile'

# Run Golang tests using Dagger
test:
    dagger -m github.com/dictybase-docker/dagger-of-dcr/golang@main call with-golang-version with-gotest-sum-formatter test --src "."


