package client

import "context"

// Server is a machine of the account. Servers and IPs are read-only.
type Server struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Type     string `json:"type"`
}

// ServerList is the answer of GET /server/list/, which is an object of three
// arrays where every other list is a bare array.
type ServerList struct {
	Web  []Server `json:"web_servers"`
	IMAP []Server `json:"imap_servers"`
	SMTP []Server `json:"smtp_servers"`
}

func (c *Client) ListServers(ctx context.Context) (*ServerList, error) {
	var out ServerList
	if err := c.get(ctx, "/server/list/", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ReadServer(ctx context.Context, id string) (*Server, error) {
	return read[Server](ctx, c, "server", id)
}

// IP is an address of a server. Type is "4" or "6", as a string.
type IP struct {
	ID      string `json:"id"`
	IP      string `json:"ip"`
	Server  string `json:"server"`
	Type    string `json:"type"`
	Primary bool   `json:"primary"`
}

func (c *Client) IPs() ReadKind[IP] {
	return ReadKind[IP]{c: c, name: "ip"}
}
