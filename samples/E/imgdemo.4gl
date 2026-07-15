#+ fglpkg sample consumer (sample-e) — demonstrates the sample-img
#+ webcomponent package installed via `fglpkg install`; images are the
#+ app's own (public/circle.png, square.png, triangle.png), the
#+ reusable widget comes from the package
IMPORT os
DEFINE prog_dir STRING

MAIN
  LET prog_dir = os.Path.dirName(arg_val(0))
  OPEN FORM f FROM "imgdemo"
  DISPLAY FORM f
  DISPLAY getUrl("circle.png") TO img
  MENU
    ON ACTION circle
      DISPLAY getUrl("circle.png") TO img
    ON ACTION square
      DISPLAY getUrl("square.png") TO img
    ON ACTION triangle
      DISPLAY getUrl("triangle.png") TO img
    COMMAND "Exit"
      EXIT MENU
  END MENU
END MAIN

FUNCTION getUrl(fname STRING)
  DEFINE url STRING
  LET url = ui.Interface.filenameToURI(os.Path.join(os.Path.join(prog_dir, "public"), fname))
  DISPLAY url TO url
  RETURN url
END FUNCTION
