FROM docker.elastic.co/kibana/kibana:6.6.0

RUN kibana-plugin install https://github.com/sivasamyk/logtrail/releases/download/v0.1.31/logtrail-6.6.0-0.1.31.zip
COPY logtrail.json plugins/logtrail/logtrail.json
