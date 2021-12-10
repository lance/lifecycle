ARG fromImage

FROM $fromImage

ARG metadata

COPY ./container/ /

LABEL io.buildpacks.lifecycle.metadata=$metadata
