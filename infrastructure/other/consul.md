# service example
```
service {
  name = "mysql-sea1"
  id  = "mysql-sea1"
  port = 3306

  check {
    id       = "mysql-check"
    name     = "MySQL TCP Check"
    tcp      = "localhost:3306"
    interval = "10s"
    timeout  = "1s"
  }
}
EOF
```

# restart
```
systemctl restart consul
```
