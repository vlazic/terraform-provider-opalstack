package client

// MariaDB and PostgreSQL have the same API shape under different paths, so one
// set of types serves both.

// DBUser is a database user.
type DBUser struct {
	Status
	ID       string `json:"id"`
	Name     string `json:"name"`
	Server   string `json:"server"`
	External bool   `json:"external"`
	// DefaultPassword is set only on the value returned by Create, when no
	// password was sent. No read returns it again.
	DefaultPassword string `json:"default_password,omitempty"`
}

type DBUserCreate struct {
	Server   string `json:"server"`
	Name     string `json:"name"`
	Password string `json:"password,omitempty"`
	External *bool  `json:"external,omitempty"`
}

type DBUserUpdate struct {
	ID       string `json:"id"`
	Password string `json:"password,omitempty"`
	External *bool  `json:"external,omitempty"`
}

// DB is a database. Users holds the ids of the database users that may write
// to it, ReadOnlyUsers of those that may only read.
type DB struct {
	Status
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Server        string   `json:"server"`
	Charset       string   `json:"charset"`
	Users         []string `json:"dbusers_readwrite"`
	ReadOnlyUsers []string `json:"dbusers_readonly"`
}

type DBCreate struct {
	Name          string   `json:"name"`
	Server        string   `json:"server"`
	Charset       string   `json:"charset,omitempty"`
	Users         []string `json:"dbusers_readwrite,omitempty"`
	ReadOnlyUsers []string `json:"dbusers_readonly,omitempty"`
}

// DBUpdate can change who has access and nothing else. For both lists nil means
// unchanged and an empty slice removes every user.
type DBUpdate struct {
	ID            string   `json:"id"`
	Users         []string `json:"dbusers_readwrite,omitzero"`
	ReadOnlyUsers []string `json:"dbusers_readonly,omitzero"`
}

type (
	DBUserKind = Kind[DBUser, DBUserCreate, DBUserUpdate]
	DBKind     = Kind[DB, DBCreate, DBUpdate]
)

func (c *Client) MariaUsers() DBUserKind {
	return kind[DBUser, DBUserCreate, DBUserUpdate](c, "mariauser")
}
func (c *Client) PsqlUsers() DBUserKind {
	return kind[DBUser, DBUserCreate, DBUserUpdate](c, "psqluser")
}
func (c *Client) MariaDBs() DBKind { return c.databases("mariadb", "mariauser") }
func (c *Client) PsqlDBs() DBKind  { return c.databases("psqldb", "psqluser") }

func (c *Client) databases(name, userKind string) DBKind {
	k := kind[DB, DBCreate, DBUpdate](c, name)
	k.refs = []ref{{path: "dbusers_readwrite", kind: userKind}, {path: "dbusers_readonly", kind: userKind}}
	return k
}
