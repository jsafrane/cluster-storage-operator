FROM registry.svc.ci.openshift.org/openshift/release:golang-1.14 AS builder
WORKDIR /go/src/github.com/openshift/cluster-storage-operator
COPY . .
RUN make

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
COPY --from=builder /go/src/github.com/openshift/cluster-storage-operator/cluster-storage-operator /usr/bin/
COPY manifests /manifests
ENTRYPOINT ["/usr/bin/cluster-storage-operator"]
LABEL io.openshift.release.operator true
LABEL io.k8s.display-name="OpenShift Cluster Storage Operator" \
      io.k8s.description="The cluster-storage-operator installs and maintains the storage components of OCP cluster."
