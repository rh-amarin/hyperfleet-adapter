helm upgrade --install adapter1 ./charts \
  -f ./charts/examples/$1/values.yaml \
  --set image.registry=quay.io/amarin \
  --set broker.googlepubsub.projectId=hcm-hyperfleet \
  --set broker.googlepubsub.subscriptionId=amarin-ns1-clusters-validation-gcp-adapter \
  --set broker.googlepubsub.topic=amarin-ns1-clusters \
  --set broker.googlepubsub.deadLetterTopic=amarin-ns1-clusters-dlq
