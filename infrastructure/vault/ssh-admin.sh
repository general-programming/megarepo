vault write ssh-client-signer/roles/administrator-role -<<EOH
{
 “allow_user_certificates”: true,
 “allowed_users”: admin,
 “allowed_extensions”: “”,
 “default_extensions”: [
 {
 “permit-pty”: “”
 }
 ],
 “key_type”: “ca”,
 “default_user”: admin,
 “ttl”: “30m0s”
}
EOH

vault write -field=signed_key ssh-client-signer/sign/administrator-role public_key=@$HOME/.ssh/id_ed25519.pub valid_principals=admin > ~/.ssh/vault-signed-key.pub
