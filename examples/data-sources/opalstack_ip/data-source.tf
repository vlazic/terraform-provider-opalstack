# The primary IPv4 address of a server, for the ip4_id of a site.
data "opalstack_ip" "v4" {
  server_id = data.opalstack_server.web.id
}
