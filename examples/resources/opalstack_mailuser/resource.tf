data "opalstack_server" "imap" {
  hostname = "imap1.us.opalstack.com"
}

resource "opalstack_mailuser" "info" {
  name           = "example_info"
  imap_server_id = data.opalstack_server.imap.id
}
