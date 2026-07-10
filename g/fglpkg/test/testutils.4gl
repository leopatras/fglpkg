#+ minimal assert based test harness for the fglpkg 4GL tests
OPTIONS SHORT CIRCUIT
IMPORT os

DEFINE _failures INT
DEFINE _checks INT

FUNCTION tEq(line INT, expr STRING, actual STRING, expected STRING)
  LET _checks = _checks + 1
  IF NVL(actual, "<NULL>") != NVL(expected, "<NULL>") THEN
    LET _failures = _failures + 1
    DISPLAY SFMT("FAIL %1:%2: %3\n  actual  :%4\n  expected:%5",
        testFile(), line, expr, NVL(actual, "<NULL>"), NVL(expected, "<NULL>"))
  END IF
END FUNCTION

FUNCTION tOk(line INT, expr STRING, ok BOOLEAN)
  LET _checks = _checks + 1
  IF NOT ok THEN
    LET _failures = _failures + 1
    DISPLAY SFMT("FAIL %1:%2: %3", testFile(), line, expr)
  END IF
END FUNCTION

FUNCTION tSummary() RETURNS INT
  IF _failures > 0 THEN
    DISPLAY SFMT("%1: %2/%3 checks FAILED", testFile(), _failures, _checks)
    RETURN 1
  END IF
  DISPLAY SFMT("%1: %2 checks OK", testFile(), _checks)
  RETURN 0
END FUNCTION

PRIVATE FUNCTION testFile() RETURNS STRING
  RETURN os.Path.baseName(arg_val(0))
END FUNCTION
