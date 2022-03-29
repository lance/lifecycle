# SKIP_BUILD_IMAGE is needed if you want to test build cache being used in subsequent build extension runs
if [ -z "$SKIP_BUILD_IMAGE" ]; then
  docker image rm $REGISTRY_HOST/extended/buildimage --force # build image to extend
  echo ">>>>>>>>>> Building build base image..."

  cat <<EOF >Dockerfile
  FROM cnbs/sample-builder:bionic
  COPY ./lifecycle /cnb/lifecycle
EOF
  docker build -t $REGISTRY_HOST/test-builder .
  docker push $REGISTRY_HOST/test-builder
fi