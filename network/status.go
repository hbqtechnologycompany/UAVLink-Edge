package network

// Status mirrors /run/dronebridge/network_status.json (writer: netmon daemon).
// Go is the single reader for app/Web — see docs/NETWORK_OWNERSHIP.md.

type PlaneEthPixhawk struct {
	Interface string `json:"interface"`
	CIDR      string `json:"cidr"`
	IP        string `json:"ip"`
	PixhawkIP string `json:"pixhawk_ip"`
	LinkUp    bool   `json:"link_up"`
}

type PlaneWlanAdmin struct {
	IP        string `json:"ip"`
	ReplyRule string `json:"reply_rule"`
}

type PlaneCloudUplink struct {
	Interface    string `json:"interface"`
	Table        int    `json:"table"`
	IP           string `json:"ip"`
	Online       bool   `json:"online"`
	Probe        string `json:"probe"`
	Ready        bool   `json:"ready"`
	SignalDBm    *int   `json:"signal_dbm"`
	SignalSource string `json:"signal_source"`
}

type Planes struct {
	EthPixhawk  PlaneEthPixhawk  `json:"eth_pixhawk"`
	WlanAdmin   PlaneWlanAdmin   `json:"wlan_admin"`
	CloudUplink PlaneCloudUplink `json:"cloud_uplink"`
}

type LinkBlock struct {
	IP          string `json:"ip"`
	Online      bool   `json:"online"`
	SignalDBm   *int   `json:"signal_dbm"`
	Signal      string `json:"signal"`
	SignalQual  string `json:"signal_quality"`
	RSRP        string `json:"rsrp"`
}

// CellularBlock extends the legacy "4g" key with driver metadata (optional).
type CellularBlock struct {
	LinkBlock
	Driver       string `json:"driver"`
	Generation   int    `json:"generation"`
	RadioAccess  string `json:"radio_access"`
}

type Status struct {
	Timestamp         int64    `json:"timestamp"`
	ActiveInterface   string   `json:"active_interface"`
	RouteReason       string   `json:"route_reason"`
	NetworkMode       string   `json:"network_mode"`
	WwanProbeMode     string   `json:"wwan_probe_mode"`
	CloudReady        bool     `json:"cloud_ready"`
	CloudSignalDBm    *int     `json:"cloud_signal_dbm"`
	CloudSignalSource string   `json:"cloud_signal_source"`
	Planes            Planes   `json:"planes"`
	FourG             LinkBlock      `json:"4g"`
	Cellular          *CellularBlock `json:"cellular,omitempty"`
	WiFi              LinkBlock      `json:"wifi"`
	SSHRoute          string   `json:"ssh_route"`
}
