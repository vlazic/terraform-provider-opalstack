# A record has no name, so it is looked up by its UUID.
data "opalstack_dnsrecord" "mx" {
  id = "11111111-1111-1111-1111-111111111111"
}

output "mx_target" {
  value = data.opalstack_dnsrecord.mx.content
}
