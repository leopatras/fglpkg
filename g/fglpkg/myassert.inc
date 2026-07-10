&ifdef FGLPKG_UTILS_MODULE
&define MYERRCALL myErr
&else
&define MYERRCALL fglpkgutils.myErr
&endif
&define MYASSERT(x) IF NOT NVL(x,0) THEN CALL MYERRCALL("ASSERTION failed in line:"||__LINE__||":"||#x) END IF
&define MYASSERT_MSG(x,msg) IF NOT NVL(x,0) THEN CALL MYERRCALL("ASSERTION failed in line:"||__LINE__||":"||#x||","||msg) END IF
&define UNUSED_VAR(variable) IF (variable) IS NULL THEN END IF
