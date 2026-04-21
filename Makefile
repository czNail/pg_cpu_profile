EXTENSION = pg_cpu_profile
MODULE_big = pg_cpu_profile
OBJS = pg_cpu_profile.o

DATA = pg_cpu_profile--1.0.sql
REGRESS = pg_cpu_profile

PG_CONFIG ?= pg_config
PGXS := $(shell $(PG_CONFIG) --pgxs)
include $(PGXS)
