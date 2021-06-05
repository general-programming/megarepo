# unrealircd
configs for various irc networks hosted on cursed infra

some nasty manual intervention required for setup

## pissnet SSL
```sh
docker run -it --rm --name certbot \
            -v "/etc/letsencrypt:/etc/letsencrypt" \
            -v "/var/lib/letsencrypt:/var/lib/letsencrypt" \
            -v "/root/cloudflare.ini:/cloudflare.ini" \
            certbot/dns-cloudflare certonly --dns-cloudflare --dns-cloudflare-credentials=/cloudflare.ini -d piss.jar.owo.me -d piss.owo.me -d pissjar.owo.me
```
