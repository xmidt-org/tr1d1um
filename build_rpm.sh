#!/bin/bash

NAME=tr1d1um

RPM_BUILD_ROOT=/root
SIGN=1
SPEC_FILE=${NAME}.spec.in


usage()
{
    echo "usage: build_rpm.sh [--rpm-build-root path] [--no-sign]"
    echo "       --spec-file      - the spec file to use - defaults to $SPEC_FILE"
    echo "       --rpm-build-root - the path where /rpmbuild exists for your user"
    echo "       --no-sign        - don't try to sign the build"
}

while [ "$1" != "" ]; do
    case $1 in
        --spec-file )       shift
                            SPEC_FILE=$1
                            ;;

        --rpm-build-root )  shift
                            RPM_BUILD_ROOT=$1
                            ;;

        --no-sign )         SIGN=0
                            ;;

        --build-number )    shift
                            BUILD_NUMBER=$1
                            ;;

        -h | --help )       usage
                            exit
                            ;;

        * )                 usage
                            exit 1

    esac
    shift
done

echo "Adjusting build number..."

taglist=`git tag -l`
tags=($taglist)

release=${tags[${#tags[@]}-1]}

if [ -z "$release"  ]; then
    echo "Could not find latest release tag!"
else
    echo "Most recent release tag: $release"
fi

release=`echo "$release" | sed -e 's/^.*\([0-9]\+\.[0-9]\+\.[0-9]\+\).*\+$/\1/'`
new_release="$release-${BUILD_NUMBER}"

echo "Issuing release $new_release..."
echo "New base version: $release..."

echo "Building the ${NAME} rpm..."

pushd ..
cp -r ${NAME} ${NAME}-$release
tar -czvf ${NAME}-$new_release.tar.gz ${NAME}-$release
mv ${NAME}-$new_release.tar.gz ${RPM_BUILD_ROOT}/rpmbuild/SOURCES
ls
echo "moving ${NAME}-$new_release.tar.gz to ${RPM_BUILD_ROOT}/rpmbuild/SOURCES"
rm -rf ${NAME}-$release
popd

# Merge the changelog into the spec file so we're consistent
cat ${SPEC_FILE} > ${NAME}.spec
cat ChangeLog >> ${NAME}.spec

if [ 0 == $SIGN ]; then
    yes "" | rpmbuild -ba \
        --define "_ver $release" \
        --define "_releaseno ${BUILD_NUMBER}" \
        --define "_fullver $new_release" \
        ${NAME}.spec
else
    yes "" | rpmbuild -ba --sign \
        --define "_signature gpg" \
        --define "_gpg_name Comcast Webpa Team <CHQSV-Webpa-Gpg@comcast.com>" \
        --define "_ver $release" \
        --define "_releaseno ${BUILD_NUMBER}" \
        --define "_fullver $new_release" \
        ${NAME}.spec
fi

pushd ..
echo "$new_release" > versionno.txt
popd
