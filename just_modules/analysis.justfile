# Build and run the analyze-roles subcommand.
# Usage: just gcp-analysis analyze-roles [--project-id <id>] --sa-name <name> --credentials <path> [--output-file <path>]
[arg("sa_name", long="sa-name", short="s", help="Service account name")]
[arg("project_id", long="project-id", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("credentials", long="credentials", short="c", help="Credentials file for the SA")]
[arg("output_file", long="output-file", short="o", help="Output file for results")]
[group('analysis')]
analyze-roles project_id="" sa_name credentials output_file="role_analysis_output.txt":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="${PROJECT_ID:-{{ project_id }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project-id."
        exit 1
    fi

    sa_email="{{ sa_name }}@${project_id}.iam.gserviceaccount.com"
    echo "Building and running analyze-roles for service account: $sa_email in project: ${project_id}"

    go build -o ./bin/gcp-tools ./cmd/gcp

    ./bin/gcp-tools analyze-roles \
        --project-id=${project_id} \
        --service-account="$sa_email" \
        --credentials={{ credentials }} \
        --output={{ output_file }}

    echo "Analysis complete. Results saved to {{ output_file }}"
