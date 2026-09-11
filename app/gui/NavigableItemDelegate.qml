import QtQuick 2.0
import QtQuick.Controls 2.2

ItemDelegate {
    property GridView grid

    highlighted: grid.activeFocus && grid.currentItem === this

    background: Rectangle {
        radius: 18
        color: parent.highlighted ? "#22c34ba9"
                                  : parent.hovered ? "#19171a"
                                                   : "transparent"
        border.width: parent.highlighted || parent.hovered ? 1 : 0
        border.color: parent.highlighted ? "#c34ba9" : "#3b363d"

        Behavior on color { ColorAnimation { duration: 140 } }
    }

    Keys.onLeftPressed: {
        grid.moveCurrentIndexLeft()
    }
    Keys.onRightPressed: {
        grid.moveCurrentIndexRight()
    }
    Keys.onDownPressed: {
        grid.moveCurrentIndexDown()
    }
    Keys.onUpPressed: {
        grid.moveCurrentIndexUp()

        // If we've reached the top of the grid, move focus to the toolbar
        if (grid.currentItem === this) {
            nextItemInFocusChain(false).forceActiveFocus(Qt.TabFocus)
        }
    }
    Keys.onReturnPressed: {
        clicked()
    }
    Keys.onEnterPressed: {
        clicked()
    }
}
