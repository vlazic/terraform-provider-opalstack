package client

type App struct {
	Status
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Server       string  `json:"server"`
	OSUser       string  `json:"osuser"`
	OSUserName   string  `json:"osuser_name"`
	Type         string  `json:"type"`
	Port         *int64  `json:"port"`
	InstallerURL *string `json:"installer_url"`
	// JSON holds the per-type settings. The spec gives the keys no schema.
	JSON map[string]any `json:"json"`
}

type AppCreate struct {
	Name         string         `json:"name"`
	OSUser       string         `json:"osuser"`
	Type         string         `json:"type"`
	InstallerURL *string        `json:"installer_url,omitempty"`
	JSON         map[string]any `json:"json,omitempty"`
}

// AppUpdate can change the type and the settings. The name, the shell user and
// the installer are fixed at creation.
type AppUpdate struct {
	ID   string `json:"id"`
	Type string `json:"type,omitempty"`
	// JSON is left out when nil. An empty, non-nil map is sent as {}.
	JSON map[string]any `json:"json,omitzero"`
}

func (c *Client) Apps() Kind[App, AppCreate, AppUpdate] {
	k := kind[App, AppCreate, AppUpdate](c, "app")
	k.refs = []ref{{path: "osuser", kind: "osuser", exempt: c.cfg.SafetyOSUserID}}
	return k
}
