#+ sample package B (fglpkg name: sample-b) — depends on A and C
PACKAGE b
IMPORT FGL a.Core AS a
IMPORT FGL c.Core AS c

--B and C depend on each other; the guard stops the mutual recursion
--(b.main -> c.main -> b.main -> ...)
DEFINE m_inCall BOOLEAN

PUBLIC FUNCTION main()
  IF m_inCall THEN
    RETURN
  END IF
  LET m_inCall = TRUE
  DISPLAY "Hello package B"
  CALL a.main()
  CALL c.main()
  LET m_inCall = FALSE
END FUNCTION
