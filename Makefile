.PHONY: dist

MINIO_BUCKET ?= mys3/vtest

dist:
	@for i in fioparse.sh run.sh fio.sh; do mc --no-color cp $$i ${MINIO_BUCKET}/bin/$$i;  done