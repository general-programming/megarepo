package main

import (
	"log"

	tunneler "github.com/general-programming/megarepo/services/infra_network_tunneler/kitex_gen/infra/network/tunneler/tunnelservice"
)

func main() {
	svr := tunneler.NewServer(new(TunnelServiceImpl))

	err := svr.Run()

	if err != nil {
		log.Println(err.Error())
	}
}
