cmake_minimum_required(VERSION 3.22)
project(audiomemo-desktop-linux LANGUAGES C CXX)
set(CMAKE_AUTOMOC ON)

find_package(Qt6 6.5 REQUIRED COMPONENTS Widgets DBus)
find_package(KF6StatusNotifierItem REQUIRED)
find_package(LayerShellQt 6.0 REQUIRED)
find_package(PkgConfig REQUIRED)
pkg_check_modules(Wayland REQUIRED IMPORTED_TARGET wayland-client)
pkg_get_variable(WAYLAND_PROTOCOLS wayland-protocols pkgdatadir)
find_program(WAYLAND_SCANNER wayland-scanner REQUIRED)

set(protocol ${WAYLAND_PROTOCOLS}/staging/ext-data-control/ext-data-control-v1.xml)
set(header ${CMAKE_CURRENT_BINARY_DIR}/ext-data-control.h)
set(code ${CMAKE_CURRENT_BINARY_DIR}/ext-data-control.c)
add_custom_command(OUTPUT ${header} ${code}
    COMMAND ${WAYLAND_SCANNER} client-header ${protocol} ${header}
    COMMAND ${WAYLAND_SCANNER} private-code ${protocol} ${code}
    DEPENDS ${protocol}
    VERBATIM)

add_library(audiomemo-wayland STATIC wayland.cpp ${header} ${code})
target_compile_features(audiomemo-wayland PUBLIC cxx_std_17)
target_include_directories(audiomemo-wayland PUBLIC ${CMAKE_CURRENT_BINARY_DIR})
target_link_libraries(audiomemo-wayland PUBLIC Qt6::Core PkgConfig::Wayland)

add_executable(audiomemo-desktop-linux main.cpp)
target_link_libraries(audiomemo-desktop-linux PRIVATE
    Qt6::Widgets Qt6::DBus KF6::StatusNotifierItem LayerShellQt::Interface audiomemo-wayland)
if(LayerShellQt_VERSION VERSION_GREATER_EQUAL 6.6)
    target_compile_definitions(audiomemo-desktop-linux PRIVATE LAYER_SHELL_EXPLICIT_ACTIVE_SCREEN)
endif()
install(TARGETS audiomemo-desktop-linux RUNTIME DESTINATION libexec)

option(AUDIOMEMO_DESKTOP_TESTS "Build isolated Linux desktop checks" OFF)
if(AUDIOMEMO_DESKTOP_TESTS)
    enable_testing()
    pkg_check_modules(WaylandServer REQUIRED IMPORTED_TARGET wayland-server)
    find_package(Python3 REQUIRED COMPONENTS Interpreter)
    set(server_header ${CMAKE_CURRENT_BINARY_DIR}/ext-data-control-server.h)
    add_custom_command(OUTPUT ${server_header}
        COMMAND ${WAYLAND_SCANNER} server-header ${protocol} ${server_header}
        DEPENDS ${protocol}
        VERBATIM)
    add_executable(wayland-test wayland_test.cpp ${server_header})
    target_link_libraries(wayland-test PRIVATE audiomemo-wayland PkgConfig::WaylandServer)
    add_test(NAME clipboard-data-control COMMAND wayland-test)
    add_test(NAME helper-headless COMMAND ${Python3_EXECUTABLE}
        ${CMAKE_CURRENT_SOURCE_DIR}/helper_test.py $<TARGET_FILE:audiomemo-desktop-linux>)
    set_tests_properties(clipboard-data-control helper-headless PROPERTIES TIMEOUT 10)
endif()
