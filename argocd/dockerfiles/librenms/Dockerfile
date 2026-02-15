FROM librenms/librenms

RUN apk --no-cache add php83-xmlwriter && \
    chown -R librenms:librenms /opt/librenms && \
    lnms plugin:add socialiteproviders/keycloak
