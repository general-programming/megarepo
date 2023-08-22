deploy_nomad:
	vault kv get -mount=webscale-scrape -format=json ansible_vars_server | jq '.data.data' >  /tmp/infra_vars.json
	ansible-playbook -i inventory/netbox_inventory.yml --limit service_nomad_http -vv oneoff_cluster.yml
