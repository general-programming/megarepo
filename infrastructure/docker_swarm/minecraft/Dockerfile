FROM openjdk:8

RUN apt-get update && \
    apt-get install -y --no-install-recommends less nano byobu openssh-server locales htop vim rsync python3 python3-pip python3-virtualenv && \
    sed -i 's/^# *\(en_US.UTF-8\)/\1/' /etc/locale.gen && \
    locale-gen && \
    echo "export PATH=$JAVA_HOME/bin:\$PATH" >> /etc/profile && \
    cp /usr/share/openssh/sshd_config /etc/system_sshd_config

ADD init.sh /init.sh

CMD /init.sh
