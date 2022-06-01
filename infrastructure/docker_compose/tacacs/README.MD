# TACACS
TACACS code from https://github.com/dchidell/docker-tacacs

## Building + Deploying
```
docker build -t registry.generalprogramming.org/tacacs --force-rm . && docker push registry.generalprogramming.org/tacacs
docker buildx build --platform linux/amd64,linux/arm64 -t registry.generalprogramming.org/tacacs:latest --force-rm --push .
docker rm -f tacacs
docker run -d --restart=always --name tacacs --env-file .env -p 10.65.67.6:49:49 tacacs
```

## Config: Cisco
```
aaa new-model

tacacs-server host 10.65.67.6 key secret
aaa authentication login default group tacacs+ local
aaa authentication enable default group tacacs+ enable

aaa authorization exec default group tacacs+ local

aaa authorization commands 0 default group tacacs+ local
aaa authorization commands 1 default group tacacs+ local
aaa authorization commands 15 default group tacacs+ local

aaa accounting exec default start-stop group tacacs+
aaa accounting commands 0 default start-stop group tacacs+
aaa accounting commands 1 default start-stop group tacacs+
aaa accounting commands 15 default start-stop group tacacs+
```

## Config: DNOS9 + Force10 MXL
```
tacacs-server host 10.65.67.6 key secret

aaa authentication login default tacacs+ local
aaa authentication enable default tacacs+ enable

aaa authorization exec default tacacs+ local
aaa accounting exec default start-stop tacacs+
aaa accounting commands 0 default start-stop tacacs+
aaa accounting commands 1 default start-stop tacacs+
aaa accounting commands 15 default start-stop tacacs+
```

## Config: Arista EOS
```
tacacs-server host 10.65.67.6 vrf internal key secret

aaa authentication login default group tacacs+ local
aaa authentication enable default group tacacs+ local
aaa authorization exec default group tacacs+ local

aaa authorization commands 0 default group tacacs+
aaa authorization commands 1 default group tacacs+
aaa authorization commands 15 default group tacacs+

aaa accounting exec default start-stop group tacacs+
aaa accounting commands 0 default start-stop group tacacs+
aaa accounting commands 1 default start-stop group tacacs+
aaa accounting commands 15 default start-stop group tacacs+
```
