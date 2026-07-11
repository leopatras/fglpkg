#+ sample package C (fglpkg name: sample-c) — depends on A and B
IMPORT FGL a
IMPORT FGL b

--B and C depend on each other; the guard stops the mutual recursion
--(c.main -> b.main -> c.main -> ...)
DEFINE m_inCall BOOLEAN

FUNCTION main()
  IF m_inCall THEN
    RETURN
  END IF
  LET m_inCall = TRUE
  DISPLAY "Hello package C"
  CALL a.main()
  CALL b.main()
  LET m_inCall = FALSE
END FUNCTION
