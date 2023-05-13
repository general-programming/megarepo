docker rm -f deluge
docker run -d \
  --name=deluge \
  -e PUID=1000 \
  -e PGID=1000 \
  -e TZ=Etc/UTC \
  -e DELUGE_LOGLEVEL=error `#optional` \
  -p 10.65.67.20:8112:8112 \
  -p 0.0.0.0:6881:6881 \
  -p 0.0.0.0:6881:6881/udp \
  -v /mnt/ceph/etc/deluge:/config \
  -v /mnt/ceph/var/torrents:/downloads \
  --restart unless-stopped \
  lscr.io/linuxserver/deluge:latest
