resource "opalstack_dnsrecord" "verification" {
  domain_id = opalstack_domain.www.id
  type      = "TXT"
  content   = "site-verification=abc123"
  ttl       = 3600
}
