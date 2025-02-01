hosts=(
    "10.3.2.16"
    "10.3.2.17"
    "10.3.2.18"
    "10.3.2.10"
    "10.3.2.11"
    "10.3.2.12"
)
for host in ${hosts[@]}
do
    echo "Upgrading $host"
    talosctl upgrade --image factory.talos.dev/installer-secureboot/ce4c980550dd2ab1b17bbf2b08801c7eb59418eafe8f279833297925d67c7515:v1.9.3 -n $host
done
