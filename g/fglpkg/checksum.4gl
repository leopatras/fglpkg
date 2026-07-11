#+ SHA-256 file digests and verification
#+ port of internal/checksum/checksum.go (pattern from gwa's gwamd5.4gl)
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT security
IMPORT FGL fglpkg.fglpkgutils
&include "myassert.inc"

--SHA256 of the empty input (security.Digest.AddData can't take empty data)
PRIVATE CONSTANT EMPTY_SHA256 =
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

#+computes the lowercase hex SHA-256 digest of a file
FUNCTION sha256File(fname STRING) RETURNS STRING
  DEFINE img BYTE
  DEFINE digest security.Digest
  DEFINE result STRING
  LOCATE img IN MEMORY
  IF NOT os.Path.exists(fname) THEN
    CALL fglpkgutils.myErr(SFMT("sha256File: '%1' doesn't exist", fname))
  END IF
  IF os.Path.size(fname) == 0 THEN
    RETURN EMPTY_SHA256
  END IF
  TRY
    CALL img.readFile(fname)
  CATCH
    CALL fglpkgutils.myErr(SFMT("sha256File: can't read file:%1", fname))
  END TRY
  TRY
    LET digest = security.Digest.CreateDigest("SHA256")
    CALL digest.AddData(img)
    LET result = digest.DoHexBinaryDigest()
  CATCH
    CALL fglpkgutils.myErr(
        SFMT("sha256File: digest of %1 failed:%2", fname, err_get(status)))
  END TRY
  FREE img
  RETURN result.toLowerCase()
END FUNCTION

#+verifies a file against an expected SHA-256 hex digest;
#+an empty expected value skips verification (trusted source)
FUNCTION verifyFile(fname STRING, expected STRING) RETURNS(BOOLEAN, STRING)
  IF expected IS NULL OR expected.trim().getLength() == 0 THEN
    RETURN TRUE, NULL
  END IF
  VAR got = sha256File(fname)
  IF NOT got.equals(expected.trim().toLowerCase()) THEN
    RETURN FALSE,
        SFMT("checksum mismatch for %1:\n  expected: %2\n  got:      %3",
            fname, expected, got)
  END IF
  RETURN TRUE, NULL
END FUNCTION
