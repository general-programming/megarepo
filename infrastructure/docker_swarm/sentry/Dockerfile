FROM getsentry/sentry:latest

WORKDIR /usr/src/sentry

COPY sentry.conf.py /etc/sentry/
COPY config.yml /etc/sentry/

# Ghetto ONBUILD
RUN mkdir -p /tmp/ && mkdir -p /var/lib/sentry/data
RUN apt-get update && apt-get install -y libsasl2-dev python-dev libldap2-dev libssl-dev build-essential
COPY requirements.txt /usr/src/sentry/
RUN pip install -r /usr/src/sentry/requirements.txt
