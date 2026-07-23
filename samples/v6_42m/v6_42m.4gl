#+ sample package v6_42m (fglpkg name: sample-v6-42m) -- same demo as
#+ sample-v6 (uses the prometheus package, introduced in Genero 6.00),
#+ but published as compiled .42m ONLY: fglpkg.json's "files" lists just
#+ "*.42m", so this source file is never shipped.
IMPORT prometheus

FUNCTION main()
  DEFINE c prometheus.Counter
  LET c = prometheus.Counter.create("sample_v6_42m_hello_total",
      "Number of hellos from sample-v6-42m", [])
  CALL c.inc([])
  DISPLAY "Hello package v6_42m: prometheus counter incremented"
END FUNCTION
