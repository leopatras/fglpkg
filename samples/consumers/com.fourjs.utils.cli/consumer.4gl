#+ consumer project com.fourjs.utils.cli -- installs com-fourjs-utils-cli
#+ (PACKAGE com.fourjs.utils.cli, registry name mismatches the PACKAGE
#+ name -- GIS-346) and calls into it
IMPORT FGL com.fourjs.utils.cli.Errors

MAIN
  DISPLAY "Hello from consumer com.fourjs.utils.cli"
  CALL Errors.myErr("demonstration error from consumer com.fourjs.utils.cli")
END MAIN
