%define debug_package %{nil}

Name:       tr1d1um
Version:    %{_ver}
Release:    %{_releaseno}%{?dist}
Summary:    The Xmidt router agent.

Group:      System Environment/Daemons
License:    ASL 2.0
URL:        https://github.com/Comcast/tr1d1um
Source0:    %{name}-%{_fullver}.tar.gz

BuildRequires:  golang >= 1.8
Requires:       supervisor

%description
The Xmidt router agent.

%prep
%setup -q


%build
export GOPATH=$(pwd)
pushd src
glide install --strip-vendor
cd tr1d1um
go build %{name}
popd

%install

# Install Binary
%{__install} -d %{buildroot}%{_bindir}
%{__install} -p src/tr1d1um/%{name} %{buildroot}%{_bindir}

# Install Service
%{__install} -d %{buildroot}%{_initddir}
%{__install} -p etc/init.d/%{name} %{buildroot}%{_initddir}

# Install Configuration
%{__install} -d %{buildroot}%{_sysconfdir}/%{name}
%{__install} -p etc/%{name}/%{name}.env.example %{buildroot}%{_sysconfdir}/%{name}/%{name}.env.example
%{__install} -p etc/%{name}/%{name}.json %{buildroot}%{_sysconfdir}/%{name}/%{name}.json
%{__install} -p etc/%{name}/supervisord.conf %{buildroot}%{_sysconfdir}/%{name}/supervisord.conf

# Create Logging Location
%{__install} -d %{buildroot}%{_localstatedir}/log/%{name}

# Create Runtime Details Location
%{__install} -d %{buildroot}%{_localstatedir}/run/%{name}

%files
%defattr(644, tr1d1um, tr1d1um, 755)

# Binary
%dir %{_bindir}
%attr(755, tr1d1um, tr1d1um) %{_bindir}/%{name} 

# Init.d
%attr(755, tr1d1um, tr1d1um) %{_initddir}/%{name}

# Configuration
%dir %{_sysconfdir}/%{name}
%config %attr(644, tr1d1um, tr1d1um) %{_sysconfdir}/%{name}/%{name}.env.example
%config %attr(644, tr1d1um, tr1d1um) %{_sysconfdir}/%{name}/%{name}.json
%config %attr(644, tr1d1um, tr1d1um) %{_sysconfdir}/%{name}/supervisord.conf

# Logging Location
%dir %{_localstatedir}/log/%{name}

# Runtime Details Location
%dir %{_localstatedir}/run/%{name}

%pre
# If app user does not exist, create
id %{name} >/dev/null 2>&1
if [ $? != 0 ]; then
    /usr/sbin/groupadd -r %{name} >/dev/null 2>&1
    /usr/sbin/useradd -d /var/run/%{name} -r -g %{name} %{name} >/dev/null 2>&1
fi


%post
if [ $1 = 1 ]; then
    /sbin/chkconfig --add %{name}
fi

%preun
# Stop service if running
if [ -e /etc/init.d/%{name} ]; then
    /sbin/service %{name} stop > /dev/null 2>&1
    true
fi

# If not an upgrade, then delete
if [ $1 = 0 ]; then
    /sbin/chkconfig --del %{name} > /dev/null 2>&1
    true
fi

%postun
# Do not remove anything if this is not an uninstall
if [ $1 = 0 ]; then
    /usr/sbin/userdel -r %{name} >/dev/null 2>&1
    /usr/sbin/groupdel %{name} >/dev/null 2>&1
    # Ignore errors from above
    true
fi

%changelog
