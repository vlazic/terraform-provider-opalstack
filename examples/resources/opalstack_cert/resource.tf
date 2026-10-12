resource "opalstack_cert" "example" {
  name = "example-com"
  cert = file("${path.module}/example.com.crt")
  key  = file("${path.module}/example.com.key")
}

resource "opalstack_site" "www" {
  name       = "www"
  domain_ids = [opalstack_domain.example.id]
  routes     = [{ app_id = opalstack_app.www.id }]
  ip4_id     = data.opalstack_ip.v4.id
  cert_id    = opalstack_cert.example.id
}
