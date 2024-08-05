#!/usr/bin/env bash

CLUSTER_FILES=( "talosconfig" "controlplane.yaml" "worker.yaml" "secrets.yaml" )
get_cluster_config() {
    CLUSTER_NAME=$1
    TALOSCONFIG=$(vault kv get -field=talosconfig secret/talos/$CLUSTER_NAME)

    if [ -z "$TALOSCONFIG" ]; then
        echo "Missing $CLUSTER_NAME/talosconfig"
        return
    fi

    echo "$TALOSCONFIG" > $CLUSTER_NAME/talosconfig
}

save_cluster_config() {
    CLUSTER_NAME=$1
    file_paths=()
    for filename in "${CLUSTER_FILES[@]}"; do
        file_paths+=("$filename=@$CLUSTER_NAME/$filename")
    done

    vault kv patch secret/talos/$CLUSTER_NAME "${file_paths[@]}"
}

ACTION=$1

# handle actions save/load
if [ "$ACTION" == "load" ]; then
    for cluster in $(vault kv get -field=clusters -format=json secret/talos/clusters | jq '.[]' -r); do
        mkdir -p $cluster
        get_cluster_config $cluster
    done
elif [ "$ACTION" == "save" ]; then
    for cluster in $(vault kv get -field=clusters -format=json secret/talos/clusters | jq '.[]' -r); do
        save_cluster_config $cluster
    done
else
    echo "Invalid action: $ACTION"
    exit 1
fi
