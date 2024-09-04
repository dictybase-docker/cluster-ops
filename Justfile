# Target to extract roles with custom project ID, service account email, and output file
extract-roles-custom project_id sa_email output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Extracting roles for service account: {{sa_email}} in project: {{project_id}}"
    echo "Output will be saved to: {{output_file}}"
    
    # Create directory for output file if it doesn't exist
    mkdir -p "$(dirname "{{output_file}}")"
    
    # Extract roles and save to output file
    gcloud projects get-iam-policy "{{project_id}}" --format=json | \
    jq -r '.bindings[] | select(.members[] | \ 
      contains("serviceAccount:{{sa_email}}")) | \
      .role' > "{{output_file}}"
    
    echo "Roles have been extracted and saved to {{output_file}}"
    echo "Summary of extracted roles:"
    cat "{{output_file}}"

# Target to output service account details in JSON format
sa-details project_id sa_email output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Fetching details for service account: {{sa_email}} in project: {{project_id}}"
    echo "Output will be saved to: {{output_file}}"
    
    # Create directory for output file if it doesn't exist
    mkdir -p "$(dirname "{{output_file}}")"
    
    # Fetch service account details and save to output file
    gcloud iam service-accounts describe "{{sa_email}}" \
    --project="{{project_id}}" \
    --format=json > "{{output_file}}"
    
    echo "Service account details have been saved to {{output_file}}"
    echo "Summary of service account details:"
    jq '.' "{{output_file}}"

