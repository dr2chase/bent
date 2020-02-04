#!/bin/bash -x

if [ $# -lt 2 ] ; then
  echo cmpjob.sh "<branch-or-tag>" "<branch-or-tag>" "bent-options"
  exit 1
fi

if [ ${1:0:1} = "-" ] ; then
  echo "First parameter should be a git tag or branch"
  exit 1
fi

if [ ${2:0:1} = "-" ] ; then
  echo "Second parameter should be a git tag or branch"
  exit 1
fi

oldtag="$1"
shift

newtag="$1"
shift

ROOT=`pwd`
export ROOT oldtag newtag

# N is number of benchmarks, B is number of builds
# Can override these with -N= and -a= on command line.
N=25
B=25

cd "${ROOT}"

if [ -e go-old ] ; then
	rm -rf go-old
fi

git clone https://go.googlesource.com/go go-old
if [ $? != 0 ] ; then
	echo git clone https://go.googlesource.com/go go-old FAILED
	exit 1
fi
cd go-old/src
git checkout ${oldtag}
if [ $? != 0 ] ; then
	echo git checkout ${oldtag} failed
	exit 1
fi
./make.bash
if [ $? != 0 ] ; then
	echo BASE make.bash FAILED
	exit 1
fi

cd "${ROOT}"

if [ -e go-new ] ; then
	rm -rf go-new
fi
git clone https://go.googlesource.com/go go-new
if [ $? != 0 ] ; then
	echo git clone go-new failed
	exit 1
fi
cd go-new/src
git checkout ${newtag}
if [ $? != 0 ] ; then
	echo git checkout ${newtag} failed
	exit 1
fi
./make.bash
if [ $? != 0 ] ; then
	echo make.bash failed
	exit 1
fi

cd "${ROOT}"
perflock bent -U -v -N=${N} -a=${B} -L=bentjobs.log -C=configurations-cmpjob.toml "$@"
RUN=`tail -1 bentjobs.log | awk -c '{print $1}'`

cd bench
STAMP="stamp-$$"
export STAMP
echo "suite: bent-cmp-branch" >> ${STAMP}
echo "bentstamp: ${RUN}" >> "${STAMP}"
echo "oldtag: ${oldtag}" >> "${STAMP}"
echo "newtag: ${newtag}" >> "${STAMP}"

oldlog="old-${oldtag}"
newlog="new-${newtag}"

cat ${RUN}.Old.build > ${oldlog}
cat ${RUN}.New.build > ${newlog}
egrep '^(Benchmark|[-_a-zA-Z0-9]+:)' ${RUN}.Old.stdout >> ${oldlog}
egrep '^(Benchmark|[-_a-zA-Z0-9]+:)' ${RUN}.New.stdout >> ${newlog}
cat ${RUN}.Old.{benchsize,benchdwarf} >> ${oldlog}
cat ${RUN}.New.{benchsize,benchdwarf} >> ${newlog}
benchsave -header "${STAMP}" "${oldlog}" "${newlog}"
rm "${STAMP}"
