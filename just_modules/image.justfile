# Create a machine image from an instance in Google Cloud.
# Usage: just gcp-image create-machine-image [--project <project-id>] --instance <name> [--backup-for <label>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("instance", long="instance", short="i", help="Source instance name")]
[arg("backup_for", long="backup-for", short="b", help="Backup label")]
[group('image-management')]
create-machine-image project="" instance backup_for="k8s-node":
    #!/usr/bin/env bash
    gcloud config set disable_prompts true
    set -euo pipefail

    project_id="${PROJECT_ID:-{{ project }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    image_name="{{ instance }}-$(date +%Y%m%d-%H%M%S)"

    source_zone=$(gcloud compute instances list --project=${project_id} --filter="name={{ instance }}" --format="value(zone)")

    if [ -z "$source_zone" ]; then
        echo "Error: Unable to determine the zone for instance {{ instance }}"
        exit 1
    fi

    echo "Creating machine image $image_name from instance {{ instance }} in project ${project_id} (zone: $source_zone)"
    gcloud compute machine-images create $image_name \
        --project=${project_id} \
        --source-instance={{ instance }} \
        --source-instance-zone=$source_zone

    echo "Machine image creation initiated. Waiting for completion..."
    gcloud compute machine-images describe $image_name \
        --project=${project_id} \
        --format="value(status)" \
        --verbosity=none | \
    while read status; do
        if [ "$status" = "READY" ]; then
            echo "Machine image $image_name created successfully."
            break
        elif [ "$status" = "FAILED" ]; then
            echo "Machine image creation failed."
            exit 1
        else
            echo "Current status: $status"
            sleep 10
        fi
    done

    echo "Machine image details:"
    gcloud compute machine-images describe $image_name \
        --project=${project_id} \
        --format="table(name,family,sourceDisk,sourceInstance,status,storageLocations,labels)"
