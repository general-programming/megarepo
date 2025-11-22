FROM librenms/librenms:latest

RUN apk add --no-cache php83-xmlwriter && \
    chown -R librenms:librenms /opt/librenms && \
    lnms plugin:add socialiteproviders/keycloak && \
    chown -R nobody:nogroup /opt/librenms
