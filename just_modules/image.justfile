# Create a machine image in Google Cloud
[group('image-management')]
create-machine-image project instance backup_for="k8s-node":
    #!/usr/bin/env bash
    set -euo pipefail
    
    # Generate image name from instance name and current date
    image_name="{{instance}}-$(date +%Y%m%d-%H%M%S)"
    
    # Extract the zone of the source instance
    source_zone=$(gcloud compute instances list --project={{project}} --filter="name={{instance}}" --format="value(zone)")
    
    if [ -z "$source_zone" ]; then
        echo "Error: Unable to determine the zone for instance {{instance}}"
        exit 1
    fi
    
    echo "Creating machine image $image_name from instance {{instance}} in project {{project}} (zone: $source_zone)"
    gcloud compute machine-images create $image_name \
        --project={{project}} \
        --source-instance={{instance}} \
        --source-instance-zone=$source_zone 
    
    echo "Machine image creation initiated. Waiting for completion..."
    gcloud compute machine-images describe $image_name \
        --project={{project}} \
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

    # Display the details of the created machine image
    echo "Machine image details:"
    gcloud compute machine-images describe $image_name \
        --project={{project}} \
        --format="table(name,family,sourceDisk,sourceInstance,status,storageLocations,labels)"
