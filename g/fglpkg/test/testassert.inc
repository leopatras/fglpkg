&define TEQ(actual, expected) CALL testutils.tEq(__LINE__, #actual, (actual), (expected))
&define TOK(cond) CALL testutils.tOk(__LINE__, #cond, NVL((cond),FALSE))
&define TSUMMARY() EXIT PROGRAM testutils.tSummary()
