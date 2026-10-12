data "opalstack_site" "www" {
  name = "www"
}

output "www_domains" {
  value = data.opalstack_site.www.domain_ids
}
