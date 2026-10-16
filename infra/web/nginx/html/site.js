// shared by every site page: the build number in the footer
fetch('/version.txt').then(function (r) { return r.ok ? r.text() : ''; }).then(function (v) {
  var el = document.getElementById('build-version');
  if (el && v && v.trim()) el.textContent = 'build ' + v.trim();
}).catch(function () {});
