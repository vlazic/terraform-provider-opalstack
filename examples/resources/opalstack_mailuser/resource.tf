data "opalstack_server" "imap" {
  hostname = "imap1.us.opalstack.com"
}

resource "opalstack_mailuser" "info" {
  name           = "example_info"
  imap_server_id = data.opalstack_server.imap.id
}

# Filter rules and an autoresponder are optional. Whatever is left out is not managed.
resource "opalstack_mailuser" "sales" {
  name           = "example_sales"
  imap_server_id = data.opalstack_server.imap.id

  procmailrc = <<-EOT
    :0
    * ^Subject:.*\[newsletter\]
    newsletters/
  EOT

  autoresponder_enable  = true
  autoresponder_subject = "Out of office"
  autoresponder_message = "We are back on Monday."
  autoresponder_noreply = ["noreply@example.com"]
}
