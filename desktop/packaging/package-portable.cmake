if(NOT DEFINED PORTABLE_SOURCE_DIR OR NOT DEFINED PORTABLE_BINARY OR
   NOT DEFINED PORTABLE_CORE_BINARY OR NOT DEFINED PORTABLE_TOAD_BINARY OR
   NOT DEFINED PORTABLE_VERSION OR NOT DEFINED PORTABLE_ARCH OR
   NOT DEFINED PORTABLE_OUTPUT_DIR OR NOT DEFINED PORTABLE_DESKTOP_FILE)
    message(FATAL_ERROR "Portable package variables are required")
endif()

set(NAME "kikimora-${PORTABLE_VERSION}-linux-${PORTABLE_ARCH}")
set(STAGE_PARENT "${CMAKE_CURRENT_BINARY_DIR}/portable")
set(STAGE_ROOT "${STAGE_PARENT}/${NAME}")
set(OUTPUT "${PORTABLE_OUTPUT_DIR}/${NAME}.tar.gz")

foreach(BINARY PORTABLE_BINARY PORTABLE_CORE_BINARY PORTABLE_TOAD_BINARY)
    if(NOT EXISTS "${${BINARY}}")
        message(FATAL_ERROR "Required portable binary was not produced: ${${BINARY}}")
    endif()
endforeach()
if(NOT EXISTS "${PORTABLE_DESKTOP_FILE}")
    message(FATAL_ERROR "Desktop entry was not generated: ${PORTABLE_DESKTOP_FILE}")
endif()

file(REMOVE_RECURSE "${STAGE_ROOT}")
file(REMOVE "${OUTPUT}")
file(MAKE_DIRECTORY "${STAGE_ROOT}/bin" "${STAGE_ROOT}/share/applications"
     "${STAGE_ROOT}/share/icons/hicolor/256x256/apps" "${PORTABLE_OUTPUT_DIR}")
file(INSTALL DESTINATION "${STAGE_ROOT}/bin" TYPE PROGRAM
     FILES "${PORTABLE_BINARY}" "${PORTABLE_CORE_BINARY}" "${PORTABLE_TOAD_BINARY}")
file(INSTALL DESTINATION "${STAGE_ROOT}/share/applications" TYPE FILE
     FILES "${PORTABLE_DESKTOP_FILE}")
file(INSTALL DESTINATION "${STAGE_ROOT}/share/icons/hicolor/256x256/apps" TYPE FILE
     FILES "${PORTABLE_SOURCE_DIR}/resources/kikimora.png")
file(INSTALL DESTINATION "${STAGE_ROOT}" TYPE FILE
     FILES "${PORTABLE_SOURCE_DIR}/README.md" "${PORTABLE_SOURCE_DIR}/../VERSION")
file(WRITE "${STAGE_ROOT}/INSTALL.txt" [=[Kikimora portable native bundle

Runtime requirements on Ubuntu/Debian:
  sudo apt install bash iproute2 libqt6core6 libqt6gui6 libqt6network6 libqt6qml6 libqt6quick6 libqt6widgets6

Run the UI:
  ./bin/kikimora-ui

The Go core and Toad binaries are included under ./bin. Configure them with
the existing Kikimora/Toad TOML files; this bundle does not overwrite system
configuration.
]=])

execute_process(
    COMMAND "${CMAKE_COMMAND}" -E tar "czf" "${OUTPUT}" --format=gnutar "${NAME}"
    WORKING_DIRECTORY "${STAGE_PARENT}"
    RESULT_VARIABLE TAR_RESULT
)
if(NOT TAR_RESULT EQUAL 0)
    message(FATAL_ERROR "Could not create portable archive: ${OUTPUT}")
endif()
message(STATUS "Built portable package: ${OUTPUT}")
