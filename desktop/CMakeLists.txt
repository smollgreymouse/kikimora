cmake_minimum_required(VERSION 3.21)
project(kikimora_ui VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)

find_package(Qt6 6.5 REQUIRED COMPONENTS Core Gui Qml Quick QuickControls2)

qt_standard_project_setup(REQUIRES 6.5)

qt_add_executable(kikimora-ui
    src/main.cpp
    src/core/CoreBackend.h
    src/core/FakeCoreBackend.cpp
    src/core/FakeCoreBackend.h
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
)

target_include_directories(kikimora-ui PRIVATE src)

target_link_libraries(kikimora-ui PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Qml
    Qt6::Quick
    Qt6::QuickControls2
)

if(APPLE)
    set_target_properties(kikimora-ui PROPERTIES MACOSX_BUNDLE TRUE)
endif()

install(TARGETS kikimora-ui
    BUNDLE DESTINATION .
    RUNTIME DESTINATION bin
)
