wget https://github.com/coturn/coturn/archive/4.5.1.1.zip
./configure --prefix=/opt/coturn
/opt/coturn/bin/turnserver -a -f --no-stun --listening-port 443 --tls-listening-port 1443 --realm kraken --user webrtc:turnpassword -v -o


[Unit]
Description=Coturn Daemon
After=network.target

[Service]
Type=simple
ExecStart=/opt/coturn/bin/turnserver -a -f --no-stun --listening-port 443 --tls-listening-port 1443 --realm kraken --user webrtc:turnpassword -v
Restart=on-failure
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
