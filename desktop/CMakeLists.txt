cmake_minimum_required(VERSION 3.21)

if(NOT DEFINED KIKIMORA_VERSION)
    file(STRINGS "${CMAKE_CURRENT_SOURCE_DIR}/../VERSION" KIKIMORA_VERSION LIMIT_COUNT 1)
endif()
if(NOT KIKIMORA_VERSION MATCHES "^[0-9]+\\.[0-9]+\\.[0-9]+$")
    message(FATAL_ERROR "Invalid KIKIMORA_VERSION: ${KIKIMORA_VERSION}")
endif()
project(kikimora_ui VERSION "${KIKIMORA_VERSION}" LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)

find_package(Qt6 6.5 REQUIRED COMPONENTS Core Gui Qml Quick QuickControls2 Network Widgets Test)

set(KIKIMORA_PACKAGE_OUTPUT_DIR "${CMAKE_CURRENT_BINARY_DIR}/dist" CACHE PATH
    "Directory for generated Debian and portable packages")
set(KIKIMORA_CORE_BINARY "" CACHE FILEPATH "Built kikimora-core binary for release packaging")
set(KIKIMORA_TOAD_BINARY "" CACHE FILEPATH "Built kikimora-toad binary for release packaging")

qt_standard_project_setup(REQUIRES 6.5)

qt_add_executable(kikimora-ui
    src/main.cpp
    src/DesktopIntegration.cpp
    src/DesktopIntegration.h
    src/SystemTheme.cpp
    src/SystemTheme.h
    src/SystemIconProvider.cpp
    src/SystemIconProvider.h
    src/core/CoreBackend.h
    src/core/FakeCoreBackend.cpp
    src/core/FakeCoreBackend.h
    src/core/RealCoreClient.cpp
    src/core/RealCoreClient.h
    src/models/RoleListModel.cpp
    src/models/RoleListModel.h
)

qt_add_resources(kikimora-ui qml_resources
    PREFIX "/"
    FILES
        qml/Main.qml
        qml/Controls/ConnectCircle.qml
        qml/Controls/RoleRow.qml
        qml/Drawers/RoleDrawer.qml
        qml/Theme/KikimoraTheme.qml
        qml/Theme/qmldir
        resources/kikimora.png
)

target_include_directories(kikimora-ui PRIVATE src)

target_link_libraries(kikimora-ui PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Qml
    Qt6::Quick
    Qt6::QuickControls2
    Qt6::Network
    Qt6::Widgets
)

if(APPLE)
    set_target_properties(kikimora-ui PROPERTIES MACOSX_BUNDLE TRUE)
endif()

install(TARGETS kikimora-ui
    BUNDLE DESTINATION .
    RUNTIME DESTINATION bin
)

if(KIKIMORA_CORE_BINARY)
    if(NOT EXISTS "${KIKIMORA_CORE_BINARY}")
        message(FATAL_ERROR "KIKIMORA_CORE_BINARY does not exist: ${KIKIMORA_CORE_BINARY}")
    endif()
    install(PROGRAMS "${KIKIMORA_CORE_BINARY}" DESTINATION bin RENAME kikimora-core)
endif()
if(KIKIMORA_TOAD_BINARY)
    if(NOT EXISTS "${KIKIMORA_TOAD_BINARY}")
        message(FATAL_ERROR "KIKIMORA_TOAD_BINARY does not exist: ${KIKIMORA_TOAD_BINARY}")
    endif()
    install(PROGRAMS "${KIKIMORA_TOAD_BINARY}" DESTINATION bin RENAME kikimora-toad)
endif()

configure_file(
    "${CMAKE_CURRENT_SOURCE_DIR}/packaging/kikimora.desktop.in"
    "${CMAKE_CURRENT_BINARY_DIR}/kikimora.desktop"
    @ONLY
)
install(FILES "${CMAKE_CURRENT_BINARY_DIR}/kikimora.desktop"
    DESTINATION share/applications
)
install(FILES resources/kikimora.png
    DESTINATION share/icons/hicolor/256x256/apps
    RENAME kikimora.png
)

set(CPACK_GENERATOR "DEB")
set(CPACK_PACKAGE_NAME "kikimora")
set(CPACK_PACKAGE_VENDOR "Kikimora contributors")
set(CPACK_PACKAGE_VERSION "${KIKIMORA_VERSION}")
set(CPACK_PACKAGE_DESCRIPTION_SUMMARY "Native Qt VPN routing client")
set(CPACK_PACKAGE_CONTACT "Kikimora contributors")
set(CPACK_PACKAGE_INSTALL_DIRECTORY "kikimora")
set(CPACK_OUTPUT_FILE_PREFIX "${KIKIMORA_PACKAGE_OUTPUT_DIR}")
set(CPACK_PACKAGING_INSTALL_PREFIX "/usr")
execute_process(
    COMMAND dpkg --print-architecture
    OUTPUT_VARIABLE KIKIMORA_ARCH
    OUTPUT_STRIP_TRAILING_WHITESPACE
    ERROR_QUIET
)
if(NOT KIKIMORA_ARCH)
    set(KIKIMORA_ARCH "amd64")
endif()
set(CPACK_DEBIAN_PACKAGE_ARCHITECTURE "${KIKIMORA_ARCH}")
set(CPACK_DEBIAN_FILE_NAME "kikimora_${KIKIMORA_VERSION}_${KIKIMORA_ARCH}.deb")
set(CPACK_DEBIAN_PACKAGE_SECTION "net")
set(CPACK_DEBIAN_PACKAGE_PRIORITY "optional")
set(CPACK_DEBIAN_PACKAGE_DEPENDS
    "bash, iproute2, libqt6core6t64 | libqt6core6, libqt6gui6t64 | libqt6gui6, libqt6network6t64 | libqt6network6, libqt6qml6 | libqt6qml6t64, libqt6quick6 | libqt6quick6t64, libqt6widgets6t64 | libqt6widgets6"
)
set(CPACK_DEBIAN_PACKAGE_SHLIBDEPS OFF)
include(CPack)

add_custom_target(package-deb
    COMMAND "${CMAKE_CPACK_COMMAND}" --config "${CMAKE_BINARY_DIR}/CPackConfig.cmake" -G DEB
    DEPENDS kikimora-ui
    USES_TERMINAL
)

add_custom_target(package-portable
    COMMAND "${CMAKE_COMMAND}"
        "-DPORTABLE_SOURCE_DIR=${CMAKE_CURRENT_SOURCE_DIR}"
        "-DPORTABLE_BINARY=$<TARGET_FILE:kikimora-ui>"
        "-DPORTABLE_CORE_BINARY=${KIKIMORA_CORE_BINARY}"
        "-DPORTABLE_TOAD_BINARY=${KIKIMORA_TOAD_BINARY}"
        "-DPORTABLE_VERSION=${KIKIMORA_VERSION}"
        "-DPORTABLE_ARCH=${KIKIMORA_ARCH}"
        "-DPORTABLE_OUTPUT_DIR=${KIKIMORA_PACKAGE_OUTPUT_DIR}"
        "-DPORTABLE_DESKTOP_FILE=${CMAKE_CURRENT_BINARY_DIR}/kikimora.desktop"
        -P "${CMAKE_CURRENT_SOURCE_DIR}/packaging/package-portable.cmake"
    DEPENDS kikimora-ui
    USES_TERMINAL
)

add_custom_target(package-release DEPENDS package-deb package-portable)

enable_testing()

qt_add_executable(real-core-client-test
    tests/RealCoreClientTest.cpp
    src/core/CoreBackend.h
    src/core/RealCoreClient.cpp
    src/core/RealCoreClient.h
    src/models/RoleListModel.cpp
    src/models/RoleListModel.h
)
target_include_directories(real-core-client-test PRIVATE src)
target_link_libraries(real-core-client-test PRIVATE
    Qt6::Core
    Qt6::Network
    Qt6::Test
)
add_test(NAME real-core-client COMMAND real-core-client-test)

qt_add_executable(real-core-e2e-test
    tests/RealCoreEndToEndTest.cpp
    src/core/CoreBackend.h
    src/core/RealCoreClient.cpp
    src/core/RealCoreClient.h
    src/models/RoleListModel.cpp
    src/models/RoleListModel.h
)
target_include_directories(real-core-e2e-test PRIVATE src)
target_link_libraries(real-core-e2e-test PRIVATE
    Qt6::Core
    Qt6::Network
    Qt6::Test
)
add_test(NAME real-core-e2e COMMAND real-core-e2e-test)

qt_add_executable(real-core-ui-test
    tests/RealCoreUiTest.cpp
    src/DesktopIntegration.cpp
    src/DesktopIntegration.h
    src/SystemIconProvider.cpp
    src/SystemIconProvider.h
    src/core/CoreBackend.h
    src/core/RealCoreClient.cpp
    src/core/RealCoreClient.h
    src/models/RoleListModel.cpp
    src/models/RoleListModel.h
)
qt_add_resources(real-core-ui-test qml_ui_test_resources
    PREFIX "/"
    FILES
        qml/Main.qml
        qml/Controls/ConnectCircle.qml
        qml/Controls/RoleRow.qml
        qml/Drawers/RoleDrawer.qml
        qml/Theme/KikimoraTheme.qml
        qml/Theme/qmldir
        resources/kikimora.png
)
target_include_directories(real-core-ui-test PRIVATE src)
target_link_libraries(real-core-ui-test PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Qml
    Qt6::Quick
    Qt6::QuickControls2
    Qt6::Network
    Qt6::Widgets
    Qt6::Test
)

qt_add_executable(ui-behavior-test
    tests/UiBehaviorTest.cpp
    src/DesktopIntegration.cpp
    src/DesktopIntegration.h
    src/SystemIconProvider.cpp
    src/SystemIconProvider.h
    src/core/CoreBackend.h
    src/core/FakeCoreBackend.cpp
    src/core/FakeCoreBackend.h
    src/models/RoleListModel.cpp
    src/models/RoleListModel.h
)
qt_add_resources(ui-behavior-test qml_behavior_test_resources
    PREFIX "/"
    FILES
        qml/Main.qml
        qml/Controls/ConnectCircle.qml
        qml/Controls/RoleRow.qml
        qml/Drawers/RoleDrawer.qml
        qml/Theme/KikimoraTheme.qml
        qml/Theme/qmldir
        resources/kikimora.png
)
target_include_directories(ui-behavior-test PRIVATE src)
target_link_libraries(ui-behavior-test PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Qml
    Qt6::Quick
    Qt6::QuickControls2
    Qt6::Network
    Qt6::Widgets
    Qt6::Test
)
add_test(NAME ui-behavior COMMAND ui-behavior-test)
set_tests_properties(ui-behavior PROPERTIES
    ENVIRONMENT "QT_QPA_PLATFORM=offscreen;QT_QUICK_BACKEND=software")

qt_add_executable(system-theme-test
    tests/SystemThemeTest.cpp
    src/SystemTheme.cpp
    src/SystemTheme.h
)
target_include_directories(system-theme-test PRIVATE src)
target_link_libraries(system-theme-test PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Widgets
    Qt6::Test
)
add_test(NAME system-theme COMMAND system-theme-test)
set_tests_properties(system-theme PROPERTIES ENVIRONMENT "QT_QPA_PLATFORM=offscreen")
