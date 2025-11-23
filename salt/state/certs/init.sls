certs_gp_root_ca:
  file.managed:
    - name: /etc/ssl/certs/General_Programming_Root.pem
    - source: salt://certs/files/root_ca.crt
    - mode: '0644'
