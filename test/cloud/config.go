package cloud

import (
	"github.com/veloxvoip/sip/pkg/config"
)

const (
	Host      = "integration.sip.livekit.cloud"
	ProjectID = "sip_integration"
	Uri       = "integration.pstn.twilio.com"
)

type IntegrationConfig struct {
	*config.Config

	RoomName string
}

func NewIntegrationConfig() (*IntegrationConfig, error) {
	c := &IntegrationConfig{
		Config: &config.Config{
			ServiceName:        "sip",
			EnableJitterBuffer: true,
		},
		RoomName: "test",
	}
	if err := c.Config.Init(); err != nil {
		return nil, err
	}
	return c, nil
}
