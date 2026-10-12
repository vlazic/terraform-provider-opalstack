package client

const kindApp = "app"

type App struct {
	ID           string  `json:"id"`
	State        string  `json:"state"`
	Ready        bool    `json:"ready"`
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

func (r AppCreate) guardTarget() guardTarget { return guardTarget{name: r.Name} }

// AppUpdate can change the type and the settings. The name, the shell user and
// the installer are fixed at creation.
type AppUpdate struct {
	ID   string         `json:"id"`
	Type string         `json:"type,omitempty"`
	JSON map[string]any `json:"json,omitempty"`
}

func (r AppUpdate) objectID() string { return r.ID }

func (c *Client) Apps() Kind[App, AppCreate, AppUpdate] {
	return Kind[App, AppCreate, AppUpdate]{c: c, name: kindApp}
}
