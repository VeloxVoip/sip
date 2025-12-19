// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/livekit/mediatransportutil/pkg/rtcconfig"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/logger/medialogutils"
	"github.com/livekit/protocol/redis"
	"github.com/livekit/protocol/utils/guid"
	"github.com/livekit/psrpc"
	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/veloxvoip/sip/pkg/errors"
	"github.com/veloxvoip/sip/pkg/ipvalidator"
)

const (
	DefaultSIPPort    int = 5060
	DefaultSIPPortTLS int = 5061
)

var (
	DefaultRTPPortRange = rtcconfig.PortRange{Start: 10000, End: 20000}
)

type TLSCert struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type TLSConfig struct {
	Port       int       `yaml:"port"`        // announced SIP signaling port
	ListenPort int       `yaml:"port_listen"` // SIP signaling port to listen on
	Certs      []TLSCert `yaml:"certs"`
	KeyLog     string    `yaml:"key_log"`
}

type SIPTrunk struct {
	ID         string   `yaml:"id"`
	Username   string   `yaml:"username"`
	Password   string   `yaml:"password"`
	Domain     string   `yaml:"domain"`
	AllowedIPs []string `yaml:"allowed_ips"`
}

// ModelConfig represents a model configuration from config.yml
type ModelConfig struct {
	ID                 string          `yaml:"id"`
	Type               string          `yaml:"type"`
	Provider           string          `yaml:"provider"`
	Model              string          `yaml:"model"`
	Name               string          `yaml:"name"`
	MaxConcurrentCalls int             `yaml:"max_concurrent_calls"`
	AnswerTimeout      time.Duration   `yaml:"answer_timeout"`
	Capabilities       []string        `yaml:"capabilities"`
	Env                []EnvVar        `yaml:"env"`
	Ultravox           *UltravoxConfig `yaml:"ultravox,omitempty"`
}

// EnvVar represents an environment variable configuration
type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// UltravoxConfig contains Ultravox-specific configuration
type UltravoxConfig struct {
	ModelName          string              `yaml:"model_name"`
	SampleRate         int                 `yaml:"sample_rate"`
	SystemPrompt       string              `yaml:"system_prompt"`
	FirstSpeaker       FirstSpeakerConfig  `yaml:"first_speaker"`
	VAD                VADConfig           `yaml:"vad"`
	InactivityMessages []InactivityMessage `yaml:"inactivity_messages"`
	Call               UltravoxCallConfig  `yaml:"call"`
}

// FirstSpeakerConfig configures the first speaker behavior
type FirstSpeakerConfig struct {
	Enabled         bool          `yaml:"enabled"`
	Uninterruptible bool          `yaml:"uninterruptible"`
	Text            string        `yaml:"text"`
	Delay           time.Duration `yaml:"delay"`
}

// VADConfig configures Voice Activity Detection
type VADConfig struct {
	TurnEndpointDelay time.Duration `yaml:"turn_endpoint_delay"`
}

// InactivityMessage represents a timed inactivity message
type InactivityMessage struct {
	Delay       time.Duration `yaml:"delay"`
	Message     string        `yaml:"message"`
	EndBehavior string        `yaml:"end_behavior"`
}

// UltravoxCallConfig contains call-level Ultravox settings
type UltravoxCallConfig struct {
	MaxDuration      time.Duration `yaml:"max_duration"`
	RecordingEnabled bool          `yaml:"recording_enabled"`
	LanguageHint     string        `yaml:"language_hint"`
}

// isIPAllowed checks if the source IP is allowed for the SIP trunk
func (t *SIPTrunk) IsIPAllowed(sourceIP string) bool {
	if validator, err := ipvalidator.NewIPValidator(t.AllowedIPs); err == nil {
		return validator.IsAllowed(sourceIP)
	}
	return false
}

type Config struct {
	Redis     *redis.RedisConfig `yaml:"redis"`      // required
	ApiKey    string             `yaml:"api_key"`    // required (env LIVEKIT_API_KEY)
	ApiSecret string             `yaml:"api_secret"` // required (env LIVEKIT_API_SECRET)
	WsUrl     string             `yaml:"ws_url"`     // required (env LIVEKIT_WS_URL)

	HealthPort         int                 `yaml:"health_port"`
	PrometheusPort     int                 `yaml:"prometheus_port"`
	PProfPort          int                 `yaml:"pprof_port"`
	SIPPort            int                 `yaml:"sip_port"`        // announced SIP signaling port
	SIPPortListen      int                 `yaml:"sip_port_listen"` // SIP signaling port to listen on
	SIPHostname        string              `yaml:"sip_hostname"`
	SIPRingingInterval time.Duration       `yaml:"sip_ringing_interval"` // from 1 sec up to 60 (default '1s')
	TLS                *TLSConfig          `yaml:"tls"`
	RTPPort            rtcconfig.PortRange `yaml:"rtp_port"`
	Logging            logger.Config       `yaml:"logging"`
	ClusterID          string              `yaml:"cluster_id"` // cluster this instance belongs to
	MaxCpuUtilization  float64             `yaml:"max_cpu_utilization"`

	UseExternalIP bool   `yaml:"use_external_ip"`
	LocalNet      string `yaml:"local_net"` // local IP net to use, e.g. 192.168.0.0/24
	NAT1To1IP     string `yaml:"nat_1_to_1_ip"`
	ListenIP      string `yaml:"listen_ip"`

	// if different from signaling IP
	MediaUseExternalIP bool   `yaml:"media_use_external_ip"`
	MediaNAT1To1IP     string `yaml:"media_nat_1_to_1_ip"`

	MediaTimeout        time.Duration   `yaml:"media_timeout"`
	MediaTimeoutInitial time.Duration   `yaml:"media_timeout_initial"`
	Codecs              map[string]bool `yaml:"codecs"`

	// HideInboundPort controls how SIP endpoint responds to unverified inbound requests.
	// Setting it to true makes SIP server silently drop INVITE requests if it gets a negative Auth or Dispatch response.
	// Doing so hides our SIP endpoint from (a low effort) port scanners.
	HideInboundPort bool `yaml:"hide_inbound_port"`
	// AddRecordRoute forces SIP to add Record-Route headers to the responses.
	AddRecordRoute bool `yaml:"add_record_route"`

	// AudioDTMF forces SIP to generate audio DTMF tones in addition to digital.
	AudioDTMF              bool    `yaml:"audio_dtmf"`
	EnableJitterBuffer     bool    `yaml:"enable_jitter_buffer"`
	EnableJitterBufferProb float64 `yaml:"enable_jitter_buffer_prob"`

	// internal
	ServiceName string `yaml:"-"`
	NodeID      string // Do not provide, will be overwritten

	// Experimental, these option might go away without notice.
	Experimental struct {
		// InboundWaitACK forces SIP to wait for an ACK to 200 OK before proceeding with the call.
		InboundWaitACK bool `yaml:"inbound_wait_ack"`
	} `yaml:"experimental"`

	SIPTrunks []SIPTrunk    `yaml:"sip_trunks"`
	Models    []ModelConfig `yaml:"models"`
}

func NewConfig(confString string) (*Config, error) {
	conf := &Config{
		ApiKey:      os.Getenv("LIVEKIT_API_KEY"),
		ApiSecret:   os.Getenv("LIVEKIT_API_SECRET"),
		WsUrl:       os.Getenv("LIVEKIT_WS_URL"),
		ServiceName: "sip",
	}
	if confString != "" {
		if err := yaml.Unmarshal([]byte(confString), conf); err != nil {
			return nil, errors.ErrCouldNotParseConfig(err)
		}
	}

	if conf.Redis == nil {
		return nil, psrpc.NewErrorf(psrpc.InvalidArgument, "redis configuration is required")
	}

	return conf, nil
}

func (c *Config) Init() error {
	c.NodeID = guid.New("NE_")

	if c.SIPPort == 0 {
		c.SIPPort = DefaultSIPPort
	}
	if c.SIPPortListen == 0 {
		c.SIPPortListen = c.SIPPort
	}
	if tc := c.TLS; tc != nil {
		if tc.Port == 0 {
			tc.Port = DefaultSIPPortTLS
		}
		if tc.ListenPort == 0 {
			tc.ListenPort = tc.Port
		}
	}
	if c.RTPPort.Start == 0 {
		c.RTPPort.Start = DefaultRTPPortRange.Start
	}
	if c.RTPPort.End == 0 {
		c.RTPPort.End = DefaultRTPPortRange.End
	}
	if c.MaxCpuUtilization <= 0 || c.MaxCpuUtilization > 1 {
		c.MaxCpuUtilization = 0.9
	}

	if err := c.InitLogger(); err != nil {
		return err
	}

	if c.UseExternalIP && c.NAT1To1IP != "" {
		return fmt.Errorf("use_external_ip and nat_1_to_1_ip can not both be set")
	}

	if c.MediaUseExternalIP && c.MediaNAT1To1IP != "" {
		return fmt.Errorf("media_use_external_ip and media_nat_1_to_1_ip can not both be set")
	}

	return nil
}

func (c *Config) InitLogger(values ...interface{}) error {
	zl, err := logger.NewZapLogger(&c.Logging)
	if err != nil {
		return err
	}

	values = append(c.GetLoggerValues(), values...)
	l := zl.WithValues(values...)
	logger.SetLogger(l, c.ServiceName)
	lksdk.SetLogger(medialogutils.NewOverrideLogger(nil))

	return nil
}

// To use with zap logger
func (c *Config) GetLoggerValues() []interface{} {
	if c.NodeID == "" {
		return nil
	}
	return []interface{}{"nodeID", c.NodeID}
}

// To use with logrus
func (c *Config) GetLoggerFields() logrus.Fields {
	fields := logrus.Fields{
		"logger": c.ServiceName,
	}
	v := c.GetLoggerValues()
	for i := 0; i < len(v); i += 2 {
		fields[v[i].(string)] = v[i+1]
	}

	return fields
}

func GetLocalIP() (netip.Addr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return netip.Addr{}, nil
	}
	type Iface struct {
		Name string
		Addr netip.Addr
	}
	var candidates []Iface
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagRunning == 0 {
			continue
		}
		if ifc.Flags&(net.FlagPointToPoint|net.FlagLoopback) != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				ip, _ := netip.AddrFromSlice(ip4)
				candidates = append(candidates, Iface{
					Name: ifc.Name, Addr: ip,
				})
				logger.Debugw("considering interface", "iface", ifc.Name, "ip", ip)
			}
		}
	}
	if len(candidates) == 0 {
		return netip.Addr{}, fmt.Errorf("no local IP found")
	}
	return candidates[0].Addr, nil
}

// GetSIPTrunkBySourceIP gets the SIP trunk for the source IP
func (c *Config) GetSIPTrunkBySourceIP(sourceIP string) *SIPTrunk {
	for _, trunk := range c.SIPTrunks {
		if validator, err := ipvalidator.NewIPValidator(trunk.AllowedIPs); err == nil {
			if validator.IsAllowed(sourceIP) {
				return &trunk
			}
		}
	}
	return nil
}

// GetSIPTrunkByName gets the SIP trunk by name
func (c *Config) GetSIPTrunkByHost(domain string) *SIPTrunk {
	for _, trunk := range c.SIPTrunks {
		if trunk.Domain == domain {
			return &trunk
		}
	}
	return nil
}

// GetModelConfig gets the model configuration by ID
func (c *Config) GetModelConfig(id string) *ModelConfig {
	for i := range c.Models {
		if c.Models[i].ID == id {
			return &c.Models[i]
		}
	}
	return nil
}

// GetUltravoxConfig gets the Ultravox configuration for a specific model
func (c *Config) GetUltravoxConfig(modelID string) *UltravoxConfig {
	model := c.GetModelConfig(modelID)
	if model == nil {
		return nil
	}
	return model.Ultravox
}
