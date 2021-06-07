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

## docker build
docker buildx build --push --platform linux/ppc64le,linux/s390x,linux/arm/v7,linux/arm64,linux/amd64 --tag quay.io/nepeat/unrealircd .
