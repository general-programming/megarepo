data "cloudflare_zones" "all_zones" {
    filter {
      status = "active"
    }
}

resource "cloudflare_email_routing_catch_all" "email_catch_all" {
    for_each = {
        for zone in data.cloudflare_zones.all_zones.zones: zone.name => zone.id
        if !contains(var.excluded_domains, zone.name)
    }

    zone_id = each.value
    name = "catch all"
    enabled = true

    matcher {
        type = "all"
    }

    action {
        type = "worker"
        value = ["mailhook"]
    }
}

resource "cloudflare_email_routing_settings" "enable_email_routing" {
    for_each = {
        for zone in data.cloudflare_zones.all_zones.zones: zone.name => zone.id
        if !contains(var.excluded_domains, zone.name)
    }

    zone_id = each.value
    enabled = "true"
}
