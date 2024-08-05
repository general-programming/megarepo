hosts=(
    "10.65.67.44"
    "10.65.67.45"
    "10.65.67.46"
    "10.65.67.47"
    "10.65.67.48"
    "10.65.67.49"
    "10.65.67.50"
    "10.65.67.51"
)
for host in ${hosts[@]}
do
    echo "Upgrading $host"
    talosctl upgrade --image ghcr.io/siderolabs/installer:v1.7.5 -n $host
done
