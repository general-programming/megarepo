# znc
swarmfile to deploy ZNC

## renew certs
```
export CF_Token=$(vault kv get -field=cf_token secret/znc)
export CF_Account_ID=$(vault kv get -field=cf_account secret/znc)
export CF_Zone_ID=$(vault kv get -field=cf_zone secret/znc)


acme.sh --issue -d erin-znc.generalprogramming.org --dns dns_cf

cp /root/.acme.sh/erin-znc.generalprogramming.org/erin-znc.generalprogramming.org.cer /srv/var/znc/tls/cert.pem
cp /root/.acme.sh/erin-znc.generalprogramming.org/erin-znc.generalprogramming.org.key /srv/var/znc/tls/cert.key
chown -Rv 100:101 /srv/var/znc/tls
```
