#!/bin/bash -x

ROOT=`pwd`
export ROOT

# BASE is the baseline, defined here, assumed checked out and built.
BASE=Go1.13
export BASE

# N is number of benchmarks, B is number of builds
# Can override these with -N= and -a= on command line.
N=25
B=25

cd "${ROOT}"

if [ ! -e "${BASE}" ] ; then
	echo Missing expected baseline directory "${BASE}" in "${ROOT}", attempting to checkout and build.
	base=${BASE,G}
	git clone https://go.googlesource.com/go -b release-branch.${base} ${BASE}
	if [ $? != 0 ] ; then
		echo git clone https://go.googlesource.com/go -b release-branch.${base} ${BASE} FAILED
		exit 1
	fi
	cd ${BASE}/src
	./make.bash
	if [ $? != 0 ] ; then
		echo BASE make.bash FAILED
		exit 1
	fi
	cd "${ROOT}"
fi

# Refresh tip, get revision
if [ -e go-tip ] ; then
	rm -rf go-tip
fi
git clone https://go.googlesource.com/go go-tip
if [ $? != 0 ] ; then
	echo git clone go-tip failed
	exit 1
fi
cd go-tip/src
./make.bash
if [ $? != 0 ] ; then
	echo make.bash failed
	exit 1
fi
tip=`git log -n 1 --format='%h'`

# Get revision for base so there is no ambiguity
cd "${ROOT}"/${BASE}
base=`git log -n 1 --format='%h'`

cd "${ROOT}"
perflock bent -U -v -N=${N} -a=${B} -L=bentjobs.log -C=configurations-cronjob.toml "$@"
RUN=`tail -1 bentjobs.log | awk -c '{print $1}'`

cd bench
STAMP="stamp-$$"
export STAMP
echo "suite: bent-cron" >> ${STAMP}
echo "bentstamp: ${RUN}" >> "${STAMP}"
echo "tip: ${tip}" >> "${STAMP}"
echo "base: ${base}" >> "${STAMP}"

cat ${RUN}.Base.build > ${BASE}
cat ${RUN}.Tip.build > ${tip}
egrep '^(Benchmark|[-_a-zA-Z0-9]+:)' ${RUN}.Base.stdout >> ${BASE}
egrep '^(Benchmark|[-_a-zA-Z0-9]+:)' ${RUN}.Tip.stdout >> ${tip}
cat ${RUN}.Base.{benchsize,benchdwarf} >> ${BASE}
cat ${RUN}.Tip.{benchsize,benchdwarf} >> ${tip}
benchsave -header "${STAMP}" ${BASE} ${tip}
rm "${STAMP}"
