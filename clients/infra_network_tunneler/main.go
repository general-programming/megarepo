package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/kitex/client"

	"github.com/general-programming/megarepo/clients/infra_network_tunneler/kitex_gen/infra/network/tunneler"
	"github.com/general-programming/megarepo/clients/infra_network_tunneler/kitex_gen/infra/network/tunneler/tunnelservice"
)

func main() {
	client := tunnelservice.MustNewClient("infra.network.tunneler", client.WithHostPorts("127.0.0.1:8888"))

	resp, err := client.GetTunnel(context.TODO(), &tunneler.GetTunnelRequest{
		Auth: &tunneler.AuthData{Authkey: "hunter2"},
	})
	if err != nil {
		fmt.Printf("err: %v\n", err)
	}
	fmt.Printf("%v\n", resp)
}
