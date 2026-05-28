requirements:
- ability to provide few dirs to watch with possibility to provide different rules:
  - log-listener -d /path/to/dir/a /path/to/dir/b -d1 /path/to/dir/c -dX -r [r:<regex>] [older:date|datetime] [younger:date|datetime] -r1 .... -rX
- ability to provide set of files to watch
- rules to mach log lines to certians paths that allows to render matched lines (example if line contaims partial json, it should be rendered as json)