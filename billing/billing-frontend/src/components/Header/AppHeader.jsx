import React from 'react';
import PropTypes from 'prop-types';
import { withStyles } from '@material-ui/core/styles';
import AppBar from '@material-ui/core/AppBar';
import Toolbar from '@material-ui/core/Toolbar';
import Typography from '@material-ui/core/Typography';
import Button from '@material-ui/core/Button';
import { Link } from 'react-router-dom';
import { connect } from 'react-redux';

import { COLOR_LIGHT, COLOR_TEXT } from '../../utils/constants';

import { logout } from '../../actions/user.action';

const styles = {
    root: {
        flexGrow: 1,
    },
    grow: {
        flexGrow: 1,
    },
    menuButton: {
        marginLeft: -12,
        marginRight: 20,
    },
    appBar: {
        boxShadow: 'none',
        backgroundColor: COLOR_LIGHT,
        color: COLOR_TEXT,
    },
};

function AppHeader(props) {
    // eslint-disable-next-line react/prop-types
    const { classes, loggedIn } = props;
    return (
        <div className={classes.root}>
            <AppBar className={classes.appBar} position="static">
                <Toolbar>
                    <Typography variant="h6" color="inherit" className={classes.grow}>
                        General Programming&#39;s Store
                    </Typography>
                    {loggedIn ? (
                        <Button
                            color="inherit"
                            onClick={logout}
                        >
                            Logout
                        </Button>
                    ) : (
                        <Button
                            color="inherit"
                            component={Link}
                            to="/login"
                        >
                        Login
                        </Button>
                    )
                    }
                </Toolbar>
            </AppBar>
        </div>
    );
}

AppHeader.propTypes = {
    // eslint-disable-next-line react/forbid-prop-types
    classes: PropTypes.object.isRequired,
};

const mapStateToProps = (state) => {
    const { user, loggedIn } = state.authentication;
    return {
        user,
        loggedIn,
    };
};

export default connect(mapStateToProps)(withStyles(styles)(AppHeader));
