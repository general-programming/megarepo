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
    talosctl upgrade --image factory.talos.dev/installer-secureboot/376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba:v1.7.5 -n $host
done
