FROM python:3-slim

RUN pip install --quiet elasticsearch-curator

ENTRYPOINT [ "/usr/local/bin/curator" ]

COPY config.yml /
COPY actions /actions/
