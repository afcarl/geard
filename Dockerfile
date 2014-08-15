FROM ubuntu:trusty
MAINTAINER Clayton Coleman <ccoleman@redhat.com>

ENV GOPATH /go
RUN apt-get update -y
&& \ apt-get install -y golang git bzr make libselinux-dev gcc pkg-config 
&& \ apt-get clean
&& \ mkdir -p $GOPATH && echo $GOPATH >> ~/.bash_profile

ADD     . /go/src/github.com/openshift/geard
WORKDIR   /go/src/github.com/openshift/geard

RUN \
   ./contrib/build -s -n && \
   ./contrib/test && \
   /bin/cp -f $GOPATH/bin/gear-auth-keys-command /usr/sbin/ && \
   /bin/cp -f $GOPATH/bin/switchns /usr/bin && \
   /bin/cp -f $GOPATH/bin/gear /usr/bin && \
   /bin/cp -f $GOPATH/bin/sti /usr/bin && \
   rm -rf $GOPATH

CMD ["/bin/gear", "daemon"]
EXPOSE 43273
VOLUME /var/lib/containers

