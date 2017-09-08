# tr1d1um

[![Build Status](https://travis-ci.org/Comcast/tr1d1um.svg?branch=master)](https://travis-ci.org/Comcast/tr1d1um) 
[![codecov.io](http://codecov.io/github/Comcast/tr1d1um/coverage.svg?branch=master)](http://codecov.io/github/Comcast/tr1d1um?branch=master)
[![Code Climate](https://codeclimate.com/github/Comcast/tr1d1um/badges/gpa.svg)](https://codeclimate.com/github/Comcast/tr1d1um)
[![Issue Count](https://codeclimate.com/github/Comcast/tr1d1um/badges/issue_count.svg)](https://codeclimate.com/github/Comcast/tr1d1um)
[![Go Report Card](https://goreportcard.com/badge/github.com/Comcast/tr1d1um)](https://goreportcard.com/report/github.com/Comcast/tr1d1um)
[![Apache V2 License](http://img.shields.io/badge/license-Apache%20V2-blue.svg)](https://github.com/Comcast/tr1d1um/blob/master/LICENSE)

The Webpa micro-service that encode TR-181 requests.

# How to Install

## Centos 6

1. Import the public GPG key (replace `v0.0.1-65alpha` with the release you want)

```
rpm --import https://github.com/Comcast/tr1d1um/releases/download/v0.0.1-65alpha/RPM-GPG-KEY-comcast-xmidt
```

2. Install the rpm with yum (so it installs any/all dependencies for you)

```
yum install https://github.com/Comcast/tr1d1um/releases/download/v0.0.1-65alpha/tr1d1um-0.0.1-65.el6.x86_64.rpm
```
