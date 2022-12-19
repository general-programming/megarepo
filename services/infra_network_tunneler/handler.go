package main

import (
	"context"

	tunneler "github.com/general-programming/megarepo/services/infra_network_tunneler/kitex_gen/infra/network/tunneler"
)

// TunnelServiceImpl implements the last service interface defined in the IDL.
type TunnelServiceImpl struct{}

// GetTunnel implements the TunnelServiceImpl interface.
func (s *TunnelServiceImpl) GetTunnel(ctx context.Context, req *tunneler.GetTunnelRequest) (resp *tunneler.GetTunnelResponse, err error) {
	resp = &tunneler.GetTunnelResponse{
		Endpoint: "asdfhjkl",
	}

	return
}
