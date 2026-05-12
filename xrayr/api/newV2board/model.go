package newV2board

import (
	"encoding/json"
	"fmt"
	"strings"
)

type serverConfig struct {
	shadowsocks
	v2ray
	trojan
	mieru

	ServerPort int `json:"server_port"`
	BaseConfig struct {
		PushInterval any `json:"push_interval"`
		PullInterval any `json:"pull_interval"`
	} `json:"base_config"`
	Routes []route `json:"routes"`
}

type shadowsocks struct {
	Cipher       string `json:"cipher"`
	Obfs         string `json:"obfs"`
	ObfsSettings struct {
		Path string `json:"path"`
		Host string `json:"host"`
	} `json:"obfs_settings"`
	ServerKey string `json:"server_key"`
}

type v2ray struct {
	Network         string `json:"network"`
	NetworkSettings struct {
		Path        string           `json:"path"`
		Host        string           `json:"host"`
		Headers     *json.RawMessage `json:"headers"`
		ServiceName string           `json:"serviceName"`
		Header      *json.RawMessage `json:"header"`
	} `json:"networkSettings"`
	VlessNetworkSettings struct {
		Path        string           `json:"path"`
		Host        string           `json:"host"`
		Headers     *json.RawMessage `json:"headers"`
		ServiceName string           `json:"serviceName"`
		Header      *json.RawMessage `json:"header"`
	} `json:"network_settings"`
	VlessFlow        string `json:"flow"`
	VlessTlsSettings struct {
		ServerPort string `json:"server_port"`
		Dest       string `json:"dest"`
		xVer       uint64 `json:"xver"`
		Sni        string `json:"server_name"`
		PrivateKey string `json:"private_key"`
		ShortId    string `json:"short_id"`
	} `json:"tls_settings"`
	Tls int `json:"tls"`
}

type trojan struct {
	Host       string `json:"host"`
	ServerName string `json:"server_name"`
}

type mieru struct {
	Transport      string `json:"transport"`
	TrafficPattern string `json:"traffic_pattern"`
}

type route struct {
	Id          int        `json:"id"`
	Match       routeMatch `json:"match"`
	Action      string     `json:"action"`
	ActionValue string     `json:"action_value"`
}

type user struct {
	Id          int    `json:"id"`
	Uuid        string `json:"uuid"`
	SpeedLimit  int    `json:"speed_limit"`
	DeviceLimit int    `json:"device_limit"`
}

type routeMatch []string

func (m *routeMatch) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single == "" {
			*m = nil
			return nil
		}
		*m = splitRouteMatch(single)
		return nil
	}

	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*m = list
		return nil
	}

	var rawList []any
	if err := json.Unmarshal(data, &rawList); err == nil {
		result := make([]string, 0, len(rawList))
		for _, raw := range rawList {
			switch v := raw.(type) {
			case string:
				result = append(result, v)
			default:
				result = append(result, fmt.Sprint(v))
			}
		}
		*m = result
		return nil
	}

	return fmt.Errorf("unsupported route match value: %s", string(data))
}

func splitRouteMatch(match string) []string {
	parts := strings.Split(match, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
