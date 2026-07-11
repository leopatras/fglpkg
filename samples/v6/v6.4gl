#+ sample package v6 (fglpkg name: sample-v6)
#+ uses the prometheus package, introduced in Genero 6.00 —
#+ hence the manifest constraint "genero": ">=6.00"
IMPORT prometheus

FUNCTION main()
  DEFINE c prometheus.Counter
  LET c = prometheus.Counter.create("sample_v6_hello_total",
      "Number of hellos from sample-v6", [])
  CALL c.inc([])
  DISPLAY "Hello package v6: prometheus counter incremented"
END FUNCTION
