# Build and run the analyze-roles subcommand
[group('analysis')]
analyze-roles project_id sa_name credentials output_file="role_analysis_output.txt":
    #!/usr/bin/env bash
    set -euo pipefail
    sa_email="{{sa_name}}@{{project_id}}.iam.gserviceaccount.com"
    echo "Building and running analyze-roles for service account: $sa_email in project: {{project_id}}"
    
    # Build the binary
    go build -o ./bin/gcp-tools ./cmd/gcp
    
    # Run the analyze-roles subcommand
    ./bin/gcp-tools analyze-roles \
        --project-id={{project_id}} \
        --service-account="$sa_email" \
        --credentials={{credentials}} \
        --output={{output_file}}
    
    echo "Analysis complete. Results saved to {{output_file}}"
