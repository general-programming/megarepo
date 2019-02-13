FROM docker.elastic.co/kibana/kibana:6.5.0

RUN kibana-plugin install https://github.com/sivasamyk/logtrail/releases/download/v0.1.30/logtrail-6.5.0-0.1.30.zip
COPY logtrail.json plugins/logtrail/logtrail.json
