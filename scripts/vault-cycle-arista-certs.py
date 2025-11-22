import hvac

THIRTY_ONE_DAYS = 60 * 60 * 24 * 31
vault = hvac.Client()


for fqdn in ["fmt2-cor-r-140752-1.generalprogramming.org"]:
    remaining_cert_time = 0

    # for cert in proxmox_node.certificates.info.get():
    #     if cert["filename"] != "pveproxy-ssl.pem":
    #         continue

    #     remaining_cert_time = cert["notafter"] - time.time()

    if remaining_cert_time > THIRTY_ONE_DAYS:
        print("Certificate for node {} is good.".format(fqdn))
        continue

    print("Updating certificate on node {}".format(fqdn))

    generate_certificate_response = vault.secrets.pki.generate_certificate(
        mount_point="pki_internal",
        name="genprog",
        common_name=fqdn,
        extra_params=dict(
            ttl="8766h",
        ),
    )
    print("Generated certificate for {}".format(fqdn))

    with open("ssl.key", "w") as f:
        f.write(generate_certificate_response["data"]["private_key"])

    with open("ssl_host.crt", "w") as f:
        f.write(generate_certificate_response["data"]["certificate"])

    with open("ssl_int.crt", "w") as f:
        f.write(generate_certificate_response["data"]["ca_chain"][0])

    print("Certificate updated on node {}".format(fqdn))
