# hv0
`docker run -d --restart=always --name consul -v /var/lib/consul:/consul/data -v /etc/consul:/consul/config --net=host -e 'CONSUL_ALLOW_PRIVILEGED_PORTS=' -e CONSUL_LOCAL_CONFIG='{"performance": {"raft_multiplier": 3}}' consul agent -server -client=172.17.0.1 -bind=192.168.3.3 -retry-join=192.168.3.3 -bootstrap-expect=3 -raft-protocol=3 -dns-port=53 -recursor=8.8.8.8 -ui`

# tcrp
`docker run -d --restart=always --name consul -v /var/lib/consul:/consul/data --net=host -e 'CONSUL_ALLOW_PRIVILEGED_PORTS=' -e CONSUL_LOCAL_CONFIG='{"performance": {"raft_multiplier": 3}}' consul agent -server -client=172.17.0.1 -bind=192.168.100.1 -retry-join=192.168.3.3 -bootstrap-expect=3 -raft-protocol=3 -dns-port=53 -recursor=8.8.8.8`

# pizza
`docker run -d --restart=always --name consul -v /var/lib/consul:/consul/data --net=host -e 'CONSUL_ALLOW_PRIVILEGED_PORTS=' -e CONSUL_LOCAL_CONFIG='{"performance": {"raft_multiplier": 3}}' consul agent -server -client=172.17.0.1 -bind=192.168.100.2 -retry-join=192.168.3.3 -bootstrap-expect=3 -raft-protocol=3 -dns-port=53 -recursor=8.8.8.8`
