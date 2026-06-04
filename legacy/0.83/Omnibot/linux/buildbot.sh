#!/bin/sh

export BOOST=/boost/boost_1_85_0
export BOOST_LIB=$BOOST/stage/lib

cd /media/sf_omnibot_svn_root/Omnibot
$BOOST/b2 -q address-model=32 release
