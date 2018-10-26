FROM docker.elastic.co/kibana/kibana:6.4.1

RUN kibana-plugin install https://github.com/sivasamyk/logtrail/releases/download/v0.1.30/logtrail-6.4.1-0.1.30.zip
COPY logtrail.json plugins/logtrail/logtrail.json
