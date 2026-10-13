data "opalstack_cert" "wildcard" {
  name = "wildcard-example-com"
}

output "wildcard_expires" {
  value = data.opalstack_cert.wildcard.exp_date
}
