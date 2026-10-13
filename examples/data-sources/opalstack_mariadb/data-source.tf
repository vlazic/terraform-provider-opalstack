data "opalstack_mariadb" "shop" {
  name = "shop"
}

output "shop_users" {
  value = data.opalstack_mariadb.shop.user_ids
}
